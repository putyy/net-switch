//go:build windows

package windows

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/putyy/net-switch/internal/config"
	network2 "github.com/putyy/net-switch/internal/network"
)

const (
	windowsVerificationAttempts = 5
	windowsVerificationDelay    = 500 * time.Millisecond
	windowsRollbackTimeout      = 30 * time.Second
	maxInterfaceAliasBytes      = 256
	maxRollbackDNSServers       = 16

	elevatedSuccess               = "0"
	elevatedAuthorizationRejected = "10"
	elevatedRollbackSucceeded     = "20"
	elevatedRollbackFailed        = "21"
	elevatedOperationFailed       = "22"
)

var interfaceIndexPattern = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)

var errWindowsAuthorizationDenied = errors.New("administrator authorization was denied or the elevated process could not start")

type Operator struct {
	mu     sync.Mutex
	runner commandRunner
	dryRun bool
}

func NewOperator(dryRun bool) *Operator {
	return &Operator{runner: systemCommandRunner{}, dryRun: dryRun}
}

func newOperator(runner commandRunner, dryRun bool) *Operator {
	return &Operator{runner: runner, dryRun: dryRun}
}

func (o *Operator) Apply(ctx context.Context, current network2.State, target config.IPv4Config) (network2.OperationResult, error) {
	return o.operate(ctx, network2.OperationApplyRule, current, target)
}

func (o *Operator) RestoreDHCP(ctx context.Context, current network2.State) (network2.OperationResult, error) {
	return o.operate(ctx, network2.OperationRestoreDHCP, current, config.IPv4Config{Mode: config.IPv4DHCP})
}

func (o *Operator) operate(ctx context.Context, action network2.OperationAction, current network2.State, target config.IPv4Config) (network2.OperationResult, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	result := network2.OperationResult{Action: action, DryRun: o.dryRun}
	plan, rollbackCommands, actualCurrent, err := o.prepare(ctx, action, current, target)
	if err != nil {
		return finishWindowsFailure(result, "operation.prepare_failed", err)
	}
	result.Plan = &plan

	comparison := network2.CompareIPv4(actualCurrent, target)
	if comparison.Comparable && comparison.Matches {
		state := cloneState(actualCurrent)
		result.Success = true
		result.Verified = true
		result.Message = "The current configuration already matches the target; no change was needed"
		result.MessageKey = "operation.already_matches"
		result.Comparison = &comparison
		result.State = &state
		result.CompletedAt = time.Now()
		return result, nil
	}

	if o.dryRun {
		encoded, encodeErr := json.Marshal(plan)
		if encodeErr != nil {
			return finishWindowsFailure(result, "operation.dry_run_encode_failed", fmt.Errorf("could not encode the dry-run operation plan: %w", encodeErr))
		}
		log.Printf("Dry-run Windows network operation plan: %s", encoded)
		result.Success = true
		result.Message = "The dry-run plan was generated without changing the system configuration"
		result.MessageKey = "operation.dry_run_ready"
		result.CompletedAt = time.Now()
		return result, nil
	}

	rollbackAttempted, rollbackSucceeded, executeErr := o.runElevated(ctx, plan.Commands, rollbackCommands)
	result.RollbackAttempted = rollbackAttempted
	result.RollbackSucceeded = rollbackSucceeded
	if executeErr != nil {
		result.Message = fmt.Sprintf("Windows network operation failed: %v", executeErr)
		result.MessageKey = "operation.execute_failed"
		if errors.Is(executeErr, errWindowsAuthorizationDenied) {
			result.MessageKey = "operation.authorization_denied"
		}
		result.CompletedAt = time.Now()
		return result, errors.New(result.Message)
	}

	verifiedState, verifiedComparison, verifyErr := o.verify(ctx, actualCurrent.Interface, target)
	result.State = &verifiedState
	result.Comparison = &verifiedComparison
	if verifyErr != nil {
		result.Message = fmt.Sprintf("The Windows network operation completed, but verification failed: %v", verifyErr)
		result.MessageKey = "operation.verify_failed"
		o.rollback(&result, rollbackCommands)
		result.CompletedAt = time.Now()
		return result, errors.New(result.Message)
	}

	result.Success = true
	result.Verified = true
	result.Message = "The network configuration was applied and verified"
	result.MessageKey = "operation.success"
	result.CompletedAt = time.Now()
	return result, nil
}

func (o *Operator) prepare(ctx context.Context, action network2.OperationAction, expected network2.State, target config.IPv4Config) (network2.OperationPlan, []network2.CommandPlan, network2.State, error) {
	interfaceIndex, err := validateWindowsIdentity(expected.Service, expected.Interface)
	if err != nil {
		return network2.OperationPlan{}, nil, network2.State{}, err
	}
	if err := target.Validate(); err != nil {
		return network2.OperationPlan{}, nil, network2.State{}, fmt.Errorf("invalid target network configuration: %w", err)
	}

	actual, err := readState(ctx, o.runner)
	if err != nil {
		return network2.OperationPlan{}, nil, network2.State{}, fmt.Errorf("read the Windows network state before applying changes: %w", err)
	}
	if actual.Status != network2.StateStatusConnected || actual.Interface != expected.Interface || actual.Service != expected.Service {
		return network2.OperationPlan{}, nil, network2.State{}, fmt.Errorf("network interface %q no longer matches adapter %q", expected.Interface, expected.Service)
	}
	if actual.SSID == "" {
		return network2.OperationPlan{}, nil, network2.State{}, errors.New("the selected Windows adapter is not a connected Wi-Fi network")
	}

	commands, err := windowsCommandsForTarget(interfaceIndex, target)
	if err != nil {
		return network2.OperationPlan{}, nil, network2.State{}, err
	}
	rollbackCommands, err := windowsCommandsForCurrentState(interfaceIndex, actual)
	if err != nil {
		return network2.OperationPlan{}, nil, network2.State{}, fmt.Errorf("could not create a safe rollback plan: %w", err)
	}
	return network2.OperationPlan{
		Action:    action,
		Service:   actual.Service,
		Interface: actual.Interface,
		Commands:  commands,
	}, rollbackCommands, actual, nil
}

func windowsCommandsForTarget(interfaceIndex uint32, target config.IPv4Config) ([]network2.CommandPlan, error) {
	switch target.Mode {
	case config.IPv4DHCP:
		return windowsDHCPCommands(interfaceIndex), nil
	case config.IPv4Static:
		prefix, err := netmaskPrefix(target.Netmask)
		if err != nil {
			return nil, err
		}
		return windowsStaticCommands(interfaceIndex, target.Address, prefix, target.Gateway, target.DNS), nil
	default:
		return nil, errors.New("invalid target IPv4 mode")
	}
}

func windowsCommandsForCurrentState(interfaceIndex uint32, current network2.State) ([]network2.CommandPlan, error) {
	var commands []network2.CommandPlan
	switch current.Mode {
	case network2.AddressModeDHCP:
		commands = windowsDHCPCommands(interfaceIndex)
	case network2.AddressModeStatic:
		prefix, err := netmaskPrefix(current.Netmask)
		if err != nil {
			return nil, fmt.Errorf("invalid original subnet mask: %w", err)
		}
		if validIPv4(current.IPv4Address) == "" || validIPv4(current.Gateway) == "" {
			return nil, errors.New("original static IPv4 configuration is incomplete")
		}
		commands = windowsStaticCommands(interfaceIndex, current.IPv4Address, prefix, current.Gateway, current.DNS)
	default:
		return nil, errors.New("original IPv4 configuration mode is unknown")
	}

	switch current.DNSMode {
	case network2.DNSModeAutomatic:
		commands[len(commands)-1] = windowsDNSCommand(interfaceIndex, nil)
	case network2.DNSModeManual:
		if err := validateWindowsDNS(current.DNS); err != nil {
			return nil, err
		}
		commands[len(commands)-1] = windowsDNSCommand(interfaceIndex, current.DNS)
	default:
		return nil, errors.New("original DNS configuration mode is unknown")
	}
	return commands, nil
}

func windowsStaticCommands(interfaceIndex uint32, address string, prefix int, gateway string, dns []string) []network2.CommandPlan {
	index := strconv.FormatUint(uint64(interfaceIndex), 10)
	return []network2.CommandPlan{
		powershellCommand(fmt.Sprintf("Set-NetIPInterface -InterfaceIndex %s -AddressFamily IPv4 -Dhcp Disabled -ErrorAction Stop", index)),
		powershellCommand(fmt.Sprintf("Get-NetIPAddress -InterfaceIndex %s -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.IPAddress -ne '127.0.0.1' } | Remove-NetIPAddress -Confirm:$false -ErrorAction SilentlyContinue", index)),
		powershellCommand(fmt.Sprintf("Get-NetRoute -InterfaceIndex %s -AddressFamily IPv4 -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue | Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue", index)),
		powershellCommand(fmt.Sprintf("New-NetIPAddress -InterfaceIndex %s -AddressFamily IPv4 -IPAddress %s -PrefixLength %d -DefaultGateway %s -ErrorAction Stop | Out-Null", index, psQuote(address), prefix, psQuote(gateway))),
		windowsDNSCommand(interfaceIndex, dns),
	}
}

func windowsDHCPCommands(interfaceIndex uint32) []network2.CommandPlan {
	index := strconv.FormatUint(uint64(interfaceIndex), 10)
	return []network2.CommandPlan{
		powershellCommand(fmt.Sprintf("Get-NetIPAddress -InterfaceIndex %s -AddressFamily IPv4 -ErrorAction SilentlyContinue | Where-Object { $_.PrefixOrigin -eq 'Manual' } | Remove-NetIPAddress -Confirm:$false -ErrorAction SilentlyContinue", index)),
		powershellCommand(fmt.Sprintf("Get-NetRoute -InterfaceIndex %s -AddressFamily IPv4 -DestinationPrefix '0.0.0.0/0' -ErrorAction SilentlyContinue | Where-Object { $_.Protocol -eq 'NetMgmt' } | Remove-NetRoute -Confirm:$false -ErrorAction SilentlyContinue", index)),
		powershellCommand(fmt.Sprintf("Set-NetIPInterface -InterfaceIndex %s -AddressFamily IPv4 -Dhcp Enabled -ErrorAction Stop", index)),
		windowsDNSCommand(interfaceIndex, nil),
	}
}

func windowsDNSCommand(interfaceIndex uint32, servers []string) network2.CommandPlan {
	index := strconv.FormatUint(uint64(interfaceIndex), 10)
	if len(servers) == 0 {
		return powershellCommand(fmt.Sprintf("Set-DnsClientServerAddress -InterfaceIndex %s -ResetServerAddresses -ErrorAction Stop", index))
	}
	quoted := make([]string, len(servers))
	for i, server := range servers {
		quoted[i] = psQuote(server)
	}
	return powershellCommand(fmt.Sprintf("Set-DnsClientServerAddress -InterfaceIndex %s -ServerAddresses @(%s) -ErrorAction Stop", index, strings.Join(quoted, ",")))
}

func powershellCommand(statement string) network2.CommandPlan {
	return network2.CommandPlan{Executable: powershellPath, Arguments: []string{"-Command", statement}}
}

func (o *Operator) runElevated(ctx context.Context, commands, rollback []network2.CommandPlan) (bool, bool, error) {
	operationScript, err := renderPowerShellStatements(commands)
	if err != nil {
		return false, false, err
	}
	innerScript := "$ErrorActionPreference='Stop'; try { " + operationScript + " } catch { exit 22 }; exit 0"
	if len(rollback) > 0 {
		rollbackScript, renderErr := renderPowerShellStatements(rollback)
		if renderErr != nil {
			return false, false, renderErr
		}
		innerScript = "$ErrorActionPreference='Stop'; try { " + operationScript + " } catch { try { " + rollbackScript + " } catch { exit 21 }; exit 20 }; exit 0"
	}
	encoded := encodePowerShell(innerScript)
	launcher := "$ErrorActionPreference='Stop'; try { $p=Start-Process -FilePath 'powershell.exe' -Verb RunAs -WindowStyle Hidden -Wait -PassThru -ArgumentList @('-NoProfile','-NonInteractive','-ExecutionPolicy','Bypass','-EncodedCommand','" + encoded + "'); [Console]::Out.Write([string]$p.ExitCode) } catch { [Console]::Out.Write('10') }"
	output, err := o.runner.Run(ctx, powershellPath, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", launcher)
	if err != nil {
		return false, false, fmt.Errorf("start the elevated Windows network operation: %w", err)
	}
	switch strings.TrimSpace(output) {
	case elevatedSuccess:
		return false, false, nil
	case elevatedAuthorizationRejected:
		return false, false, errWindowsAuthorizationDenied
	case elevatedRollbackSucceeded:
		return true, true, errors.New("a network command failed; the original configuration was restored")
	case elevatedRollbackFailed:
		return true, false, errors.New("a network command failed and the original configuration could not be restored")
	case elevatedOperationFailed:
		return false, false, errors.New("an elevated Windows network command failed")
	default:
		return false, false, fmt.Errorf("the elevated helper returned an unknown result %q", strings.TrimSpace(output))
	}
}

func (o *Operator) rollback(result *network2.OperationResult, commands []network2.CommandPlan) {
	result.RollbackAttempted = true
	rollbackCtx, cancel := context.WithTimeout(context.Background(), windowsRollbackTimeout)
	defer cancel()
	_, _, err := o.runElevated(rollbackCtx, commands, nil)
	result.RollbackSucceeded = err == nil
	if err != nil {
		result.Message += fmt.Sprintf("; restoring the original Windows configuration failed: %v", err)
	} else {
		result.Message += "; restoration of the original Windows configuration was attempted"
	}
}

func renderPowerShellStatements(commands []network2.CommandPlan) (string, error) {
	if len(commands) == 0 {
		return "", errors.New("the Windows network command plan is empty")
	}
	statements := make([]string, len(commands))
	for i, command := range commands {
		if command.Executable != powershellPath || len(command.Arguments) != 2 || command.Arguments[0] != "-Command" || strings.TrimSpace(command.Arguments[1]) == "" {
			return "", fmt.Errorf("refusing to execute unauthorized Windows network command at step %d", i+1)
		}
		statements[i] = command.Arguments[1]
	}
	return strings.Join(statements, "; "), nil
}

func (o *Operator) verify(ctx context.Context, interfaceName string, target config.IPv4Config) (network2.State, network2.ConfigurationComparison, error) {
	var lastState network2.State
	var lastComparison network2.ConfigurationComparison
	var lastErr error
	for attempt := 0; attempt < windowsVerificationAttempts; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(windowsVerificationDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return lastState, lastComparison, ctx.Err()
			case <-timer.C:
			}
		}

		lastState, lastErr = readState(ctx, o.runner)
		if lastErr != nil || lastState.Interface != interfaceName {
			continue
		}
		lastComparison = network2.CompareIPv4(lastState, target)
		if lastComparison.Comparable && lastComparison.Matches {
			return lastState, lastComparison, nil
		}
		lastErr = errors.New(lastComparison.Message)
	}
	if lastErr == nil {
		lastErr = errors.New("the verified Windows configuration does not match the target")
	}
	return lastState, lastComparison, lastErr
}

func validateWindowsIdentity(service, interfaceName string) (uint32, error) {
	if service == "" || strings.TrimSpace(service) != service || len(service) > maxInterfaceAliasBytes || !utf8.ValidString(service) || strings.ContainsFunc(service, unicode.IsControl) {
		return 0, errors.New("invalid Windows network adapter name")
	}
	if !interfaceIndexPattern.MatchString(interfaceName) {
		return 0, errors.New("invalid Windows network interface index")
	}
	parsed, err := strconv.ParseUint(interfaceName, 10, 32)
	if err != nil || parsed == 0 {
		return 0, errors.New("invalid Windows network interface index")
	}
	return uint32(parsed), nil
}

func validateWindowsDNS(servers []string) error {
	if len(servers) == 0 || len(servers) > maxRollbackDNSServers {
		return errors.New("invalid original manual DNS server count")
	}
	for _, server := range servers {
		address, err := netip.ParseAddr(server)
		if err != nil || !address.Is4() || address.IsUnspecified() || address.IsMulticast() {
			return fmt.Errorf("invalid original DNS address %q", server)
		}
	}
	return nil
}

func netmaskPrefix(value string) (int, error) {
	parsed := net.ParseIP(value).To4()
	if parsed == nil {
		return 0, errors.New("invalid IPv4 subnet mask")
	}
	ones, bits := net.IPMask(parsed).Size()
	if bits != 32 || ones == 0 {
		return 0, errors.New("invalid IPv4 subnet mask")
	}
	return ones, nil
}

func psQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func encodePowerShell(script string) string {
	codeUnits := utf16.Encode([]rune(script))
	encoded := make([]byte, len(codeUnits)*2)
	for i, value := range codeUnits {
		binary.LittleEndian.PutUint16(encoded[i*2:], value)
	}
	return base64.StdEncoding.EncodeToString(encoded)
}

func cloneState(state network2.State) network2.State {
	state.DNS = append([]string(nil), state.DNS...)
	if state.DNS == nil {
		state.DNS = []string{}
	}
	return state
}

func finishWindowsFailure(result network2.OperationResult, key string, err error) (network2.OperationResult, error) {
	result.Message = err.Error()
	result.MessageKey = key
	result.CompletedAt = time.Now()
	return result, err
}

//go:build darwin

package darwin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/putyy/net-switch/internal/config"
	network2 "github.com/putyy/net-switch/internal/network"
)

const (
	rollbackTimeout       = 30 * time.Second
	verificationAttempts  = 3
	verificationDelay     = 300 * time.Millisecond
	maxServiceNameBytes   = 256
	maxRollbackDNSServers = 16
)

var interfaceNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

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
	plan, rollbackCommands, err := o.prepare(ctx, action, current, target)
	if err != nil {
		result.Message = err.Error()
		result.MessageKey = "operation.prepare_failed"
		result.CompletedAt = time.Now()
		return result, err
	}
	result.Plan = &plan

	comparison := network2.CompareIPv4(current, target)
	if comparison.Comparable && comparison.Matches {
		result.Success = true
		result.Verified = true
		result.Message = "The current configuration already matches the target; no change was needed"
		result.MessageKey = "operation.already_matches"
		result.Comparison = &comparison
		state := cloneOperationState(current)
		result.State = &state
		result.CompletedAt = time.Now()
		return result, nil
	}

	if o.dryRun {
		encoded, encodeErr := json.Marshal(plan)
		if encodeErr != nil {
			result.Message = "Could not encode the dry-run operation plan"
			result.MessageKey = "operation.dry_run_encode_failed"
			result.CompletedAt = time.Now()
			return result, fmt.Errorf("%s: %w", result.Message, encodeErr)
		}
		log.Printf("Dry-run network operation plan: %s", encoded)
		result.Success = true
		result.Message = "The dry-run plan was generated without changing the system configuration"
		result.MessageKey = "operation.dry_run_ready"
		result.CompletedAt = time.Now()
		return result, nil
	}

	executed, executeErr := o.runCommands(ctx, plan.Commands)
	if executeErr != nil {
		result.Message = fmt.Sprintf("Network operation failed: %v", executeErr)
		result.MessageKey = "operation.execute_failed"
		if executed > 0 {
			o.rollback(&result, rollbackCommands)
		}
		result.CompletedAt = time.Now()
		return result, errors.New(result.Message)
	}

	verifiedState, verifiedComparison, verifyErr := o.verify(ctx, current.Service, current.Interface, target)
	result.State = &verifiedState
	result.Comparison = &verifiedComparison
	if verifyErr != nil {
		result.Message = fmt.Sprintf("The network operation completed, but verification failed: %v", verifyErr)
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

func (o *Operator) prepare(ctx context.Context, action network2.OperationAction, current network2.State, target config.IPv4Config) (network2.OperationPlan, []network2.CommandPlan, error) {
	if err := validateNetworkIdentity(current.Service, current.Interface); err != nil {
		return network2.OperationPlan{}, nil, err
	}
	if err := target.Validate(); err != nil {
		return network2.OperationPlan{}, nil, fmt.Errorf("invalid target network configuration: %w", err)
	}

	output, err := o.runner.Run(ctx, networkSetupPath, "-listnetworkserviceorder")
	if err != nil {
		return network2.OperationPlan{}, nil, fmt.Errorf("read network services before applying changes: %w", err)
	}
	services, err := parseServiceOrder(output)
	if err != nil {
		return network2.OperationPlan{}, nil, fmt.Errorf("parse network services before applying changes: %w", err)
	}
	activeService, ok := serviceForInterface(services, current.Interface)
	if !ok || activeService.Name != current.Service || !isWiFi(activeService.HardwarePort) {
		return network2.OperationPlan{}, nil, fmt.Errorf("network service %q no longer matches Wi-Fi interface %q", current.Service, current.Interface)
	}

	commands := commandsForTarget(current.Service, target)
	rollbackCommands, err := commandsForCurrentState(current)
	if err != nil {
		return network2.OperationPlan{}, nil, fmt.Errorf("could not create a safe rollback plan: %w", err)
	}
	return network2.OperationPlan{
		Action:    action,
		Service:   current.Service,
		Interface: current.Interface,
		Commands:  commands,
	}, rollbackCommands, nil
}

func commandsForTarget(serviceName string, target config.IPv4Config) []network2.CommandPlan {
	commands := make([]network2.CommandPlan, 0, 2)
	if target.Mode == config.IPv4Static {
		commands = append(commands, networkSetupCommand("-setmanual", serviceName, target.Address, target.Netmask, target.Gateway))
	} else {
		commands = append(commands, networkSetupCommand("-setdhcp", serviceName))
	}
	commands = append(commands, dnsCommand(serviceName, target.DNS))
	return commands
}

func commandsForCurrentState(current network2.State) ([]network2.CommandPlan, error) {
	commands := make([]network2.CommandPlan, 0, 2)
	switch current.Mode {
	case network2.AddressModeStatic:
		original := config.IPv4Config{
			Mode:    config.IPv4Static,
			Address: current.IPv4Address,
			Netmask: current.Netmask,
			Gateway: current.Gateway,
		}
		if err := original.Validate(); err != nil {
			return nil, fmt.Errorf("original static IPv4 configuration is incomplete: %w", err)
		}
		commands = append(commands, networkSetupCommand("-setmanual", current.Service, original.Address, original.Netmask, original.Gateway))
	case network2.AddressModeDHCP:
		commands = append(commands, networkSetupCommand("-setdhcp", current.Service))
	default:
		return nil, errors.New("original IPv4 configuration mode is unknown")
	}

	switch current.DNSMode {
	case network2.DNSModeAutomatic:
		commands = append(commands, dnsCommand(current.Service, nil))
	case network2.DNSModeManual:
		if err := validateRollbackDNS(current.DNS); err != nil {
			return nil, err
		}
		commands = append(commands, dnsCommand(current.Service, current.DNS))
	default:
		return nil, errors.New("original DNS configuration mode is unknown")
	}
	return commands, nil
}

func networkSetupCommand(arguments ...string) network2.CommandPlan {
	return network2.CommandPlan{
		Executable: networkSetupPath,
		Arguments:  append([]string(nil), arguments...),
	}
}

func dnsCommand(serviceName string, servers []string) network2.CommandPlan {
	arguments := []string{"-setdnsservers", serviceName}
	if len(servers) == 0 {
		arguments = append(arguments, "Empty")
	} else {
		arguments = append(arguments, servers...)
	}
	return networkSetupCommand(arguments...)
}

func (o *Operator) runCommands(ctx context.Context, commands []network2.CommandPlan) (int, error) {
	for index, command := range commands {
		if command.Executable != networkSetupPath {
			return index, fmt.Errorf("refusing to execute unauthorized program %q", command.Executable)
		}
		if _, err := o.runner.Run(ctx, command.Executable, command.Arguments...); err != nil {
			return index, fmt.Errorf("step %d: %w", index+1, err)
		}
	}
	return len(commands), nil
}

func (o *Operator) verify(ctx context.Context, serviceName, interfaceName string, target config.IPv4Config) (network2.State, network2.ConfigurationComparison, error) {
	var lastState network2.State
	var lastComparison network2.ConfigurationComparison
	var lastErr error
	for attempt := 0; attempt < verificationAttempts; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(verificationDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return lastState, lastComparison, ctx.Err()
			case <-timer.C:
			}
		}

		lastState, lastErr = o.readConfiguredState(ctx, serviceName, interfaceName)
		if lastErr != nil {
			continue
		}
		lastComparison = network2.CompareIPv4(lastState, target)
		if lastComparison.Comparable && lastComparison.Matches {
			return lastState, lastComparison, nil
		}
		lastErr = errors.New(lastComparison.Message)
	}
	if lastErr == nil {
		lastErr = errors.New("the verified configuration does not match the target")
	}
	return lastState, lastComparison, lastErr
}

func (o *Operator) readConfiguredState(ctx context.Context, serviceName, interfaceName string) (network2.State, error) {
	state := network2.State{
		Status:    network2.StateStatusConnected,
		Service:   serviceName,
		Interface: interfaceName,
		Mode:      network2.AddressModeUnknown,
		DNSMode:   network2.DNSModeUnknown,
		DNS:       []string{},
	}
	infoOutput, err := o.runner.Run(ctx, networkSetupPath, "-getinfo", serviceName)
	if err != nil {
		return state, fmt.Errorf("read back IPv4 configuration: %w", err)
	}
	applyNetworkInfo(&state, infoOutput)
	dnsOutput, err := o.runner.Run(ctx, networkSetupPath, "-getdnsservers", serviceName)
	if err != nil {
		return state, fmt.Errorf("read back DNS configuration: %w", err)
	}
	state.DNS, state.DNSMode = parseDNSConfiguration(dnsOutput)
	return state, nil
}

func (o *Operator) rollback(result *network2.OperationResult, commands []network2.CommandPlan) {
	result.RollbackAttempted = true
	rollbackCtx, cancel := context.WithTimeout(context.Background(), rollbackTimeout)
	defer cancel()
	_, err := o.runCommands(rollbackCtx, commands)
	result.RollbackSucceeded = err == nil
	if err != nil {
		result.Message += fmt.Sprintf("; restoring the original configuration failed: %v", err)
	} else {
		result.Message += "; restoration of the original configuration was attempted"
	}
}

func validateNetworkIdentity(serviceName, interfaceName string) error {
	if serviceName == "" || strings.TrimSpace(serviceName) != serviceName || len(serviceName) > maxServiceNameBytes || !utf8.ValidString(serviceName) || strings.ContainsFunc(serviceName, unicode.IsControl) {
		return errors.New("invalid network service name")
	}
	if !interfaceNamePattern.MatchString(interfaceName) {
		return errors.New("invalid network interface name")
	}
	return nil
}

func validateRollbackDNS(servers []string) error {
	if len(servers) == 0 || len(servers) > maxRollbackDNSServers {
		return errors.New("invalid original manual DNS server count")
	}
	for _, server := range servers {
		address, err := netip.ParseAddr(server)
		if err != nil || address.IsUnspecified() || address.IsMulticast() {
			return fmt.Errorf("invalid original DNS address %q", server)
		}
	}
	return nil
}

func cloneOperationState(state network2.State) network2.State {
	state.DNS = append([]string(nil), state.DNS...)
	if state.DNS == nil {
		state.DNS = []string{}
	}
	return state
}

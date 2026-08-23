//go:build linux

package linux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
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
	linuxRollbackTimeout      = 30 * time.Second
	linuxVerificationAttempts = 5
	linuxVerificationDelay    = 500 * time.Millisecond
	maxLinuxServiceNameBytes  = 256
	maxProfileValueBytes      = 4096
)

var (
	linuxInterfacePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,31}$`)
	connectionUUIDPattern = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)
)

type Operator struct {
	mu     sync.Mutex
	runner commandRunner
	dryRun bool
}

type connectionSnapshot struct {
	Method        string
	Addresses     string
	Gateway       string
	DNS           string
	IgnoreAutoDNS string
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
	plan, rollbackCommands, actualCurrent, connectionUUID, err := o.prepare(ctx, action, current, target)
	if err != nil {
		return finishLinuxFailure(result, "operation.prepare_failed", err)
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
			return finishLinuxFailure(result, "operation.dry_run_encode_failed", fmt.Errorf("could not encode the dry-run operation plan: %w", encodeErr))
		}
		log.Printf("Dry-run Linux network operation plan: %s", encoded)
		result.Success = true
		result.Message = "The dry-run plan was generated without changing the system configuration"
		result.MessageKey = "operation.dry_run_ready"
		result.CompletedAt = time.Now()
		return result, nil
	}

	executed, executeErr := o.runCommands(ctx, plan.Commands)
	if executeErr != nil {
		result.Message = fmt.Sprintf("Linux network operation failed: %v", executeErr)
		result.MessageKey = "operation.execute_failed"
		if isLinuxAuthorizationError(executeErr) {
			result.MessageKey = "operation.authorization_denied"
		}
		if executed > 0 {
			o.rollback(&result, rollbackCommands)
		}
		result.CompletedAt = time.Now()
		return result, errors.New(result.Message)
	}

	verifiedState, verifiedComparison, verifyErr := o.verify(ctx, actualCurrent.Interface, connectionUUID, target)
	result.State = &verifiedState
	result.Comparison = &verifiedComparison
	if verifyErr != nil {
		result.Message = fmt.Sprintf("The Linux network operation completed, but verification failed: %v", verifyErr)
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

func (o *Operator) prepare(ctx context.Context, action network2.OperationAction, expected network2.State, target config.IPv4Config) (network2.OperationPlan, []network2.CommandPlan, network2.State, string, error) {
	if err := validateLinuxIdentity(expected.Service, expected.Interface); err != nil {
		return network2.OperationPlan{}, nil, network2.State{}, "", err
	}
	if err := target.Validate(); err != nil {
		return network2.OperationPlan{}, nil, network2.State{}, "", fmt.Errorf("invalid target network configuration: %w", err)
	}

	actual, err := readState(ctx, o.runner)
	if err != nil {
		return network2.OperationPlan{}, nil, network2.State{}, "", fmt.Errorf("read the Linux network state before applying changes: %w", err)
	}
	if actual.Status != network2.StateStatusConnected || actual.Interface != expected.Interface || actual.Service != expected.Service || actual.SSID == "" {
		return network2.OperationPlan{}, nil, network2.State{}, "", fmt.Errorf("network interface %q no longer matches Wi-Fi connection %q", expected.Interface, expected.Service)
	}

	identity, err := o.readConnectionIdentity(ctx, actual.Interface)
	if err != nil {
		return network2.OperationPlan{}, nil, network2.State{}, "", err
	}
	if identity.Name != actual.Service {
		return network2.OperationPlan{}, nil, network2.State{}, "", fmt.Errorf("active connection changed from %q to %q", actual.Service, identity.Name)
	}

	snapshot, err := o.readConnectionSnapshot(ctx, identity.UUID)
	if err != nil {
		return network2.OperationPlan{}, nil, network2.State{}, "", err
	}
	commands, err := linuxCommandsForTarget(identity.UUID, actual.Interface, target)
	if err != nil {
		return network2.OperationPlan{}, nil, network2.State{}, "", err
	}
	rollbackCommands, err := linuxCommandsForSnapshot(identity.UUID, actual.Interface, snapshot)
	if err != nil {
		return network2.OperationPlan{}, nil, network2.State{}, "", fmt.Errorf("could not create a safe rollback plan: %w", err)
	}

	return network2.OperationPlan{
		Action:    action,
		Service:   actual.Service,
		Interface: actual.Interface,
		Commands:  commands,
	}, rollbackCommands, actual, identity.UUID, nil
}

func (o *Operator) readConnectionIdentity(ctx context.Context, interfaceName string) (connectionIdentity, error) {
	output, err := o.runner.Run(ctx, nmcliPath, "-t", "-e", "yes", "-f", "GENERAL.CONNECTION,GENERAL.CON-UUID", "device", "show", interfaceName)
	if err != nil {
		return connectionIdentity{}, fmt.Errorf("read the active NetworkManager connection: %w", err)
	}
	identity := parseConnectionIdentity(output)
	if identity.Name == "" || !connectionUUIDPattern.MatchString(identity.UUID) {
		return connectionIdentity{}, errors.New("NetworkManager returned an invalid active connection identity")
	}
	return identity, nil
}

func (o *Operator) readConnectionSnapshot(ctx context.Context, uuid string) (connectionSnapshot, error) {
	output, err := o.runner.Run(ctx, nmcliPath, "-t", "-e", "yes", "-f", "ipv4.method,ipv4.addresses,ipv4.gateway,ipv4.dns,ipv4.ignore-auto-dns", "connection", "show", "uuid", uuid)
	if err != nil {
		return connectionSnapshot{}, fmt.Errorf("read the current NetworkManager IPv4 profile: %w", err)
	}
	properties := parseProperties(output)
	snapshot := connectionSnapshot{
		Method:        firstProperty(properties, "ipv4.method"),
		Addresses:     joinProperty(properties, "ipv4.addresses"),
		Gateway:       firstProperty(properties, "ipv4.gateway"),
		DNS:           joinProperty(properties, "ipv4.dns"),
		IgnoreAutoDNS: firstProperty(properties, "ipv4.ignore-auto-dns"),
	}
	if err := snapshot.validate(); err != nil {
		return connectionSnapshot{}, err
	}
	return snapshot, nil
}

func (s connectionSnapshot) validate() error {
	if s.Method != "auto" && s.Method != "manual" {
		return errors.New("the original NetworkManager IPv4 method is not auto or manual")
	}
	if s.IgnoreAutoDNS == "" {
		s.IgnoreAutoDNS = "no"
	}
	if s.IgnoreAutoDNS != "yes" && s.IgnoreAutoDNS != "no" {
		return errors.New("the original NetworkManager DNS mode is invalid")
	}
	for _, value := range []string{s.Addresses, s.Gateway, s.DNS} {
		if len(value) > maxProfileValueBytes || strings.ContainsFunc(value, unicode.IsControl) {
			return errors.New("the original NetworkManager IPv4 profile contains an invalid value")
		}
	}
	if s.Method == "manual" && strings.TrimSpace(s.Addresses) == "" {
		return errors.New("the original static NetworkManager profile has no IPv4 address")
	}
	return nil
}

func linuxCommandsForTarget(uuid, interfaceName string, target config.IPv4Config) ([]network2.CommandPlan, error) {
	modifyArguments := []string{"connection", "modify", "uuid", uuid}
	switch target.Mode {
	case config.IPv4DHCP:
		modifyArguments = append(modifyArguments,
			"ipv4.method", "auto",
			"ipv4.addresses", "",
			"ipv4.gateway", "",
			"ipv4.dns", "",
			"ipv4.ignore-auto-dns", "no",
		)
	case config.IPv4Static:
		prefix, err := netmaskPrefix(target.Netmask)
		if err != nil {
			return nil, err
		}
		ignoreAutoDNS := "no"
		if len(target.DNS) > 0 {
			ignoreAutoDNS = "yes"
		}
		modifyArguments = append(modifyArguments,
			"ipv4.method", "manual",
			"ipv4.addresses", fmt.Sprintf("%s/%d", target.Address, prefix),
			"ipv4.gateway", target.Gateway,
			"ipv4.dns", strings.Join(target.DNS, ","),
			"ipv4.ignore-auto-dns", ignoreAutoDNS,
		)
	default:
		return nil, errors.New("invalid target IPv4 mode")
	}
	return []network2.CommandPlan{
		nmcliCommand(modifyArguments...),
		nmcliCommand("connection", "up", "uuid", uuid, "ifname", interfaceName),
	}, nil
}

func linuxCommandsForSnapshot(uuid, interfaceName string, snapshot connectionSnapshot) ([]network2.CommandPlan, error) {
	if snapshot.IgnoreAutoDNS == "" {
		snapshot.IgnoreAutoDNS = "no"
	}
	if err := snapshot.validate(); err != nil {
		return nil, err
	}
	return []network2.CommandPlan{
		nmcliCommand(
			"connection", "modify", "uuid", uuid,
			"ipv4.method", snapshot.Method,
			"ipv4.addresses", snapshot.Addresses,
			"ipv4.gateway", snapshot.Gateway,
			"ipv4.dns", snapshot.DNS,
			"ipv4.ignore-auto-dns", snapshot.IgnoreAutoDNS,
		),
		nmcliCommand("connection", "up", "uuid", uuid, "ifname", interfaceName),
	}, nil
}

func nmcliCommand(arguments ...string) network2.CommandPlan {
	return network2.CommandPlan{Executable: nmcliPath, Arguments: append([]string(nil), arguments...)}
}

func (o *Operator) runCommands(ctx context.Context, commands []network2.CommandPlan) (int, error) {
	for index, command := range commands {
		if command.Executable != nmcliPath {
			return index, fmt.Errorf("refusing to execute unauthorized program %q", command.Executable)
		}
		if _, err := o.runner.Run(ctx, command.Executable, command.Arguments...); err != nil {
			return index, fmt.Errorf("step %d: %w", index+1, err)
		}
	}
	return len(commands), nil
}

func (o *Operator) rollback(result *network2.OperationResult, commands []network2.CommandPlan) {
	result.RollbackAttempted = true
	rollbackCtx, cancel := context.WithTimeout(context.Background(), linuxRollbackTimeout)
	defer cancel()
	_, err := o.runCommands(rollbackCtx, commands)
	result.RollbackSucceeded = err == nil
	if err != nil {
		result.Message += fmt.Sprintf("; restoring the original NetworkManager profile failed: %v", err)
	} else {
		result.Message += "; restoration of the original NetworkManager profile was attempted"
	}
}

func (o *Operator) verify(ctx context.Context, interfaceName, connectionUUID string, target config.IPv4Config) (network2.State, network2.ConfigurationComparison, error) {
	var lastState network2.State
	var lastComparison network2.ConfigurationComparison
	var lastErr error
	for attempt := 0; attempt < linuxVerificationAttempts; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(linuxVerificationDelay)
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
		identity, identityErr := o.readConnectionIdentity(ctx, interfaceName)
		if identityErr != nil || identity.UUID != connectionUUID {
			lastErr = errors.New("the active NetworkManager connection changed during verification")
			continue
		}
		lastComparison = network2.CompareIPv4(lastState, target)
		if lastComparison.Comparable && lastComparison.Matches {
			return lastState, lastComparison, nil
		}
		lastErr = errors.New(lastComparison.Message)
	}
	if lastErr == nil {
		lastErr = errors.New("the verified Linux configuration does not match the target")
	}
	return lastState, lastComparison, lastErr
}

func validateLinuxIdentity(serviceName, interfaceName string) error {
	if serviceName == "" || strings.TrimSpace(serviceName) != serviceName || len(serviceName) > maxLinuxServiceNameBytes || !utf8.ValidString(serviceName) || strings.ContainsFunc(serviceName, unicode.IsControl) {
		return errors.New("invalid NetworkManager connection name")
	}
	if !linuxInterfacePattern.MatchString(interfaceName) {
		return errors.New("invalid Linux network interface name")
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

func joinProperty(properties map[string][]string, key string) string {
	values := properties[key]
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && value != "--" {
			filtered = append(filtered, value)
		}
	}
	return strings.Join(filtered, ",")
}

func cloneState(state network2.State) network2.State {
	state.DNS = append([]string(nil), state.DNS...)
	if state.DNS == nil {
		state.DNS = []string{}
	}
	return state
}

func finishLinuxFailure(result network2.OperationResult, key string, err error) (network2.OperationResult, error) {
	result.Message = err.Error()
	result.MessageKey = key
	result.CompletedAt = time.Now()
	return result, err
}

func isLinuxAuthorizationError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, fragment := range []string{"not authorized", "not authorised", "permission denied", "insufficient privileges", "authorization failed"} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

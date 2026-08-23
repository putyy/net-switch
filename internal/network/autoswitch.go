package network

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/putyy/net-switch/internal/config"
)

type AutoSwitchRuleSource interface {
	Snapshot() config.Config
}

type AutoSwitchOperator interface {
	Apply(context.Context, State, config.IPv4Config) (OperationResult, error)
	RestoreDHCP(context.Context, State) (OperationResult, error)
}

type AutoSwitchOutcome struct {
	Status AutoSwitchStatus
	Result *OperationResult
}

type AutoSwitcher struct {
	rules    AutoSwitchRuleSource
	operator AutoSwitchOperator

	stateMu         sync.Mutex
	failedAttemptID string
	manualOverride  string
}

func NewAutoSwitcher(rules AutoSwitchRuleSource, operator AutoSwitchOperator) (*AutoSwitcher, error) {
	if rules == nil {
		return nil, errors.New("automatic switch rule source is required")
	}
	if operator == nil {
		return nil, errors.New("automatic switch network operator is required")
	}
	return &AutoSwitcher{rules: rules, operator: operator}, nil
}

func (s *AutoSwitcher) Reconcile(ctx context.Context, current State) (AutoSwitchOutcome, error) {
	status := AutoSwitchStatus{SSID: current.SSID}
	configuration := s.rules.Snapshot()
	if !configuration.General.AutoSwitch {
		s.Reset()
		status.Decision = AutoSwitchDisabled
		status.Message = "Automatic switching is paused; only network status will be updated"
		status.MessageKey = "auto.disabled"
		return finishAutoSwitch(status), nil
	}
	if current.Status != StateStatusConnected || current.SSID == "" {
		s.Reset()
		status.Decision = AutoSwitchUnavailable
		status.Message = "No Wi-Fi network is available for automatic switching"
		status.MessageKey = "auto.unavailable"
		return finishAutoSwitch(status), nil
	}
	if s.manualOverrideActive(current) {
		status.Decision = AutoSwitchSuppressed
		status.Success = true
		status.Message = "DHCP was restored manually for this Wi-Fi connection; automatic changes are temporarily suppressed"
		status.MessageKey = "auto.suppressed"
		return finishAutoSwitch(status), nil
	}

	matched, ok := matchAutoSwitchRule(configuration.Rules, current.SSID)
	if ok {
		status.MatchedRuleID = matched.ID
		status.MatchedRule = matched.Name
		comparison := CompareIPv4(current, matched.IPv4)
		if !comparison.Comparable {
			status.Decision = AutoSwitchUnavailable
			status.Message = comparison.Message
			status.MessageKey = comparison.MessageKey
			return finishAutoSwitch(status), nil
		}
		if comparison.Matches {
			s.clearFailure()
			status.Decision = AutoSwitchMatched
			status.Success = true
			status.Message = "The current configuration already matches the rule"
			status.MessageKey = "auto.matched"
			return finishAutoSwitch(status), nil
		}
		if current.Service == "" || current.Interface == "" {
			status.Decision = AutoSwitchUnavailable
			status.Message = "Wi-Fi service information is incomplete, so the rule cannot be applied automatically"
			status.MessageKey = "auto.apply_incomplete"
			return finishAutoSwitch(status), nil
		}
		attemptID := autoSwitchAttemptID(current, matched.ID, matched.IPv4)
		if s.failureBlocked(attemptID) {
			status.Decision = AutoSwitchFailed
			status.Message = "The previous automatic attempt failed; further retries are paused to avoid repeated changes"
			status.MessageKey = "auto.apply_blocked"
			return finishAutoSwitch(status), nil
		}

		status.Attempted = true
		result, err := s.operator.Apply(ctx, current, matched.IPv4)
		result.Trigger = OperationTriggerAutomatic
		result.RuleID = matched.ID
		result.RuleName = matched.Name
		status.Success = result.Success && err == nil
		status.Message = result.Message
		status.MessageKey = result.MessageKey
		if err != nil {
			s.rememberFailure(attemptID)
			status.Decision = AutoSwitchFailed
			outcome := finishAutoSwitch(status)
			outcome.Result = &result
			return outcome, err
		}
		s.clearFailure()
		status.Decision = AutoSwitchApplied
		outcome := finishAutoSwitch(status)
		outcome.Result = &result
		return outcome, nil
	}

	if configuration.General.UnmatchedAction == config.UnmatchedKeep {
		s.clearFailure()
		status.Decision = AutoSwitchKept
		status.Success = true
		status.Message = "No rule matches this Wi-Fi; the current configuration was kept"
		status.MessageKey = "auto.kept"
		return finishAutoSwitch(status), nil
	}

	dhcpTarget := config.IPv4Config{Mode: config.IPv4DHCP}
	comparison := CompareIPv4(current, dhcpTarget)
	if !comparison.Comparable {
		status.Decision = AutoSwitchUnavailable
		status.Message = comparison.Message
		status.MessageKey = comparison.MessageKey
		return finishAutoSwitch(status), nil
	}
	if comparison.Matches {
		s.clearFailure()
		status.Decision = AutoSwitchMatched
		status.Success = true
		status.Message = "No rule matches this Wi-Fi; DHCP and automatic DNS are already active"
		status.MessageKey = "auto.dhcp_active"
		return finishAutoSwitch(status), nil
	}
	if current.Service == "" || current.Interface == "" {
		status.Decision = AutoSwitchUnavailable
		status.Message = "Wi-Fi service information is incomplete, so DHCP cannot be restored automatically"
		status.MessageKey = "auto.restore_incomplete"
		return finishAutoSwitch(status), nil
	}
	attemptID := autoSwitchAttemptID(current, "unmatched-dhcp", dhcpTarget)
	if s.failureBlocked(attemptID) {
		status.Decision = AutoSwitchFailed
		status.Message = "The previous automatic DHCP restore failed; further retries are paused"
		status.MessageKey = "auto.restore_blocked"
		return finishAutoSwitch(status), nil
	}

	status.Attempted = true
	result, err := s.operator.RestoreDHCP(ctx, current)
	result.Trigger = OperationTriggerAutomatic
	status.Success = result.Success && err == nil
	status.Message = result.Message
	status.MessageKey = result.MessageKey
	if err != nil {
		s.rememberFailure(attemptID)
		status.Decision = AutoSwitchFailed
		outcome := finishAutoSwitch(status)
		outcome.Result = &result
		return outcome, err
	}
	s.clearFailure()
	status.Decision = AutoSwitchRestored
	outcome := finishAutoSwitch(status)
	outcome.Result = &result
	return outcome, nil
}

func (s *AutoSwitcher) Reset() {
	s.stateMu.Lock()
	s.failedAttemptID = ""
	s.manualOverride = ""
	s.stateMu.Unlock()
}

func (s *AutoSwitcher) SuppressCurrentNetwork(current State) {
	s.stateMu.Lock()
	s.manualOverride = autoSwitchNetworkID(current)
	s.stateMu.Unlock()
}

func (s *AutoSwitcher) failureBlocked(attemptID string) bool {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return attemptID != "" && s.failedAttemptID == attemptID
}

func (s *AutoSwitcher) rememberFailure(attemptID string) {
	s.stateMu.Lock()
	s.failedAttemptID = attemptID
	s.stateMu.Unlock()
}

func (s *AutoSwitcher) clearFailure() {
	s.stateMu.Lock()
	s.failedAttemptID = ""
	s.stateMu.Unlock()
}

func (s *AutoSwitcher) manualOverrideActive(current State) bool {
	networkID := autoSwitchNetworkID(current)
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.manualOverride == "" {
		return false
	}
	if s.manualOverride == networkID {
		return true
	}
	s.manualOverride = ""
	return false
}

func autoSwitchAttemptID(current State, targetID string, target config.IPv4Config) string {
	return strings.Join([]string{
		current.SSID,
		current.Service,
		current.Interface,
		targetID,
		string(target.Mode),
		target.Address,
		target.Netmask,
		target.Gateway,
		strings.Join(target.DNS, ","),
	}, "\x00")
}

func autoSwitchNetworkID(current State) string {
	return strings.Join([]string{current.SSID, current.Service, current.Interface}, "\x00")
}

func matchAutoSwitchRule(rules []config.Rule, ssid string) (config.Rule, bool) {
	for _, configuredRule := range rules {
		if configuredRule.Enabled && configuredRule.SSID == ssid {
			return configuredRule, true
		}
	}
	return config.Rule{}, false
}

func finishAutoSwitch(status AutoSwitchStatus) AutoSwitchOutcome {
	status.CheckedAt = time.Now()
	return AutoSwitchOutcome{Status: status}
}

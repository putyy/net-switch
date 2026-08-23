package network

import (
	"context"
	"errors"
	"testing"

	"github.com/putyy/net-switch/internal/config"
)

type fakeAutoSwitchRules struct {
	configuration config.Config
}

func (r *fakeAutoSwitchRules) Snapshot() config.Config { return r.configuration }

type fakeAutoSwitchOperator struct {
	applyCalls   int
	restoreCalls int
	applyErr     error
}

func (o *fakeAutoSwitchOperator) Apply(_ context.Context, _ State, _ config.IPv4Config) (OperationResult, error) {
	o.applyCalls++
	result := OperationResult{Action: OperationApplyRule, Success: o.applyErr == nil, Verified: o.applyErr == nil, Message: "规则已应用"}
	if o.applyErr != nil {
		result.Message = "规则应用失败"
	}
	return result, o.applyErr
}

func (o *fakeAutoSwitchOperator) RestoreDHCP(_ context.Context, _ State) (OperationResult, error) {
	o.restoreCalls++
	return OperationResult{Action: OperationRestoreDHCP, Success: true, Verified: true, Message: "DHCP 已恢复"}, nil
}

func TestAutoSwitcherDoesNothingWhenDisabled(t *testing.T) {
	rules := autoSwitchRules(config.GeneralSettings{AutoSwitch: false, UnmatchedAction: config.UnmatchedDHCP})
	operator := &fakeAutoSwitchOperator{}
	switcher, err := NewAutoSwitcher(rules, operator)
	if err != nil {
		t.Fatalf("创建自动切换器失败: %v", err)
	}

	outcome, err := switcher.Reconcile(context.Background(), automaticDHCPState("Office-WiFi"))
	if err != nil || outcome.Status.Decision != AutoSwitchDisabled || outcome.Result != nil || operator.applyCalls != 0 || operator.restoreCalls != 0 {
		t.Fatalf("暂停状态仍执行了网络操作: outcome=%#v err=%v", outcome, err)
	}
}

func TestAutoSwitcherSkipsMatchingRuleConfiguration(t *testing.T) {
	rules := autoSwitchRules(config.GeneralSettings{AutoSwitch: true, UnmatchedAction: config.UnmatchedKeep})
	rules.configuration.Rules = []config.Rule{{
		ID: "office", Name: "公司", SSID: "Office-WiFi", Enabled: true,
		IPv4: config.IPv4Config{Mode: config.IPv4DHCP},
	}}
	operator := &fakeAutoSwitchOperator{}
	switcher, _ := NewAutoSwitcher(rules, operator)

	outcome, err := switcher.Reconcile(context.Background(), automaticDHCPState("Office-WiFi"))
	if err != nil || outcome.Status.Decision != AutoSwitchMatched || outcome.Result != nil || operator.applyCalls != 0 {
		t.Fatalf("相同配置未被跳过: outcome=%#v err=%v", outcome, err)
	}
}

func TestAutoSwitcherAppliesDifferentMatchedRule(t *testing.T) {
	rules := autoSwitchRules(config.GeneralSettings{AutoSwitch: true, UnmatchedAction: config.UnmatchedKeep})
	rules.configuration.Rules = []config.Rule{{
		ID: "office", Name: "公司", SSID: "Office-WiFi", Enabled: true,
		IPv4: config.IPv4Config{
			Mode: config.IPv4Static, Address: "192.168.10.88", Netmask: "255.255.255.0", Gateway: "192.168.10.1",
		},
	}}
	operator := &fakeAutoSwitchOperator{}
	switcher, _ := NewAutoSwitcher(rules, operator)

	outcome, err := switcher.Reconcile(context.Background(), automaticDHCPState("Office-WiFi"))
	if err != nil || outcome.Result == nil || outcome.Result.Trigger != OperationTriggerAutomatic || outcome.Result.RuleID != "office" || operator.applyCalls != 1 {
		t.Fatalf("匹配规则未自动应用: outcome=%#v err=%v", outcome, err)
	}
}

func TestAutoSwitcherKeepsUnmatchedConfiguration(t *testing.T) {
	rules := autoSwitchRules(config.GeneralSettings{AutoSwitch: true, UnmatchedAction: config.UnmatchedKeep})
	operator := &fakeAutoSwitchOperator{}
	switcher, _ := NewAutoSwitcher(rules, operator)

	outcome, err := switcher.Reconcile(context.Background(), automaticDHCPState("Guest-WiFi"))
	if err != nil || outcome.Status.Decision != AutoSwitchKept || outcome.Result != nil || operator.restoreCalls != 0 {
		t.Fatalf("未匹配保持策略错误: outcome=%#v err=%v", outcome, err)
	}
}

func TestAutoSwitcherRestoresDHCPForUnmatchedStaticNetwork(t *testing.T) {
	rules := autoSwitchRules(config.GeneralSettings{AutoSwitch: true, UnmatchedAction: config.UnmatchedDHCP})
	operator := &fakeAutoSwitchOperator{}
	switcher, _ := NewAutoSwitcher(rules, operator)
	state := automaticDHCPState("Guest-WiFi")
	state.Mode = AddressModeStatic
	state.DNSMode = DNSModeManual
	state.DNS = []string{"1.1.1.1"}

	outcome, err := switcher.Reconcile(context.Background(), state)
	if err != nil || outcome.Status.Decision != AutoSwitchRestored || outcome.Result == nil || outcome.Result.Trigger != OperationTriggerAutomatic || operator.restoreCalls != 1 {
		t.Fatalf("未匹配网络未恢复 DHCP: outcome=%#v err=%v", outcome, err)
	}
}

func TestAutoSwitcherPreservesAutomaticFailureResult(t *testing.T) {
	rules := autoSwitchRules(config.GeneralSettings{AutoSwitch: true, UnmatchedAction: config.UnmatchedKeep})
	rules.configuration.Rules = []config.Rule{{
		ID: "office", Name: "公司", SSID: "Office-WiFi", Enabled: true,
		IPv4: config.IPv4Config{
			Mode: config.IPv4Static, Address: "192.168.10.88", Netmask: "255.255.255.0", Gateway: "192.168.10.1",
		},
	}}
	operator := &fakeAutoSwitchOperator{applyErr: errors.New("authorization denied")}
	switcher, _ := NewAutoSwitcher(rules, operator)

	state := automaticDHCPState("Office-WiFi")
	outcome, err := switcher.Reconcile(context.Background(), state)
	if err == nil || outcome.Status.Decision != AutoSwitchFailed || outcome.Status.Success || outcome.Result == nil || outcome.Result.Trigger != OperationTriggerAutomatic {
		t.Fatalf("自动应用失败结果未保留: outcome=%#v err=%v", outcome, err)
	}

	blocked, blockedErr := switcher.Reconcile(context.Background(), state)
	if blockedErr != nil || blocked.Result != nil || operator.applyCalls != 1 || blocked.Status.Decision != AutoSwitchFailed {
		t.Fatalf("相同目标失败后仍被自动重试: outcome=%#v err=%v calls=%d", blocked, blockedErr, operator.applyCalls)
	}

	switcher.Reset()
	_, _ = switcher.Reconcile(context.Background(), state)
	if operator.applyCalls != 2 {
		t.Fatalf("重置失败熔断后未重新尝试: calls=%d", operator.applyCalls)
	}
}

func TestAutoSwitcherHonorsManualOverrideForCurrentNetwork(t *testing.T) {
	rules := autoSwitchRules(config.GeneralSettings{AutoSwitch: true, UnmatchedAction: config.UnmatchedKeep})
	rules.configuration.Rules = []config.Rule{{
		ID: "office", Name: "公司", SSID: "Office-WiFi", Enabled: true,
		IPv4: config.IPv4Config{
			Mode: config.IPv4Static, Address: "192.168.10.88", Netmask: "255.255.255.0", Gateway: "192.168.10.1",
		},
	}}
	operator := &fakeAutoSwitchOperator{}
	switcher, _ := NewAutoSwitcher(rules, operator)
	state := automaticDHCPState("Office-WiFi")
	switcher.SuppressCurrentNetwork(state)

	outcome, err := switcher.Reconcile(context.Background(), state)
	if err != nil || outcome.Status.Decision != AutoSwitchSuppressed || outcome.Result != nil || operator.applyCalls != 0 {
		t.Fatalf("手动恢复后仍自动应用规则: outcome=%#v err=%v", outcome, err)
	}

	switcher.Reset()
	_, _ = switcher.Reconcile(context.Background(), state)
	if operator.applyCalls != 1 {
		t.Fatalf("重置手动覆盖后未恢复自动判断: calls=%d", operator.applyCalls)
	}
}

func autoSwitchRules(general config.GeneralSettings) *fakeAutoSwitchRules {
	return &fakeAutoSwitchRules{configuration: config.Config{General: general}}
}

func automaticDHCPState(ssid string) State {
	return State{
		Status: StateStatusConnected, SSID: ssid, Service: "Wi-Fi", Interface: "en0",
		Mode: AddressModeDHCP, DNSMode: DNSModeAutomatic, DNS: []string{"192.168.1.1"},
	}
}

package network

import "time"

type AddressMode string

type DNSMode string

type OperationAction string

type OperationTrigger string

type AutoSwitchDecision string

type StateStatus string

const (
	AddressModeUnknown AddressMode = "unknown"
	AddressModeDHCP    AddressMode = "dhcp"
	AddressModeStatic  AddressMode = "static"

	DNSModeUnknown   DNSMode = "unknown"
	DNSModeAutomatic DNSMode = "automatic"
	DNSModeManual    DNSMode = "manual"

	OperationApplyRule   OperationAction = "apply_rule"
	OperationRestoreDHCP OperationAction = "restore_dhcp"

	OperationTriggerManual    OperationTrigger = "manual"
	OperationTriggerAutomatic OperationTrigger = "automatic"

	AutoSwitchDisabled    AutoSwitchDecision = "disabled"
	AutoSwitchSuppressed  AutoSwitchDecision = "suppressed"
	AutoSwitchUnavailable AutoSwitchDecision = "unavailable"
	AutoSwitchMatched     AutoSwitchDecision = "matched"
	AutoSwitchKept        AutoSwitchDecision = "kept"
	AutoSwitchApplied     AutoSwitchDecision = "applied"
	AutoSwitchRestored    AutoSwitchDecision = "restored"
	AutoSwitchFailed      AutoSwitchDecision = "failed"

	StateStatusUnknown      StateStatus = "unknown"
	StateStatusConnected    StateStatus = "connected"
	StateStatusDisconnected StateStatus = "disconnected"
	StateStatusUnavailable  StateStatus = "unavailable"
)

type State struct {
	Status      StateStatus `json:"status"`
	Message     string      `json:"message,omitempty"`
	MessageKey  string      `json:"message_key,omitempty"`
	SSID        string      `json:"ssid"`
	Service     string      `json:"service"`
	Interface   string      `json:"interface"`
	IPv4Address string      `json:"ipv4_address"`
	Netmask     string      `json:"netmask"`
	Gateway     string      `json:"gateway"`
	DNS         []string    `json:"dns"`
	DNSMode     DNSMode     `json:"dns_mode"`
	Mode        AddressMode `json:"mode"`
}

type RuntimeState struct {
	Network          State                    `json:"network"`
	MatchedRuleID    string                   `json:"matched_rule_id,omitempty"`
	TargetComparison *ConfigurationComparison `json:"target_comparison,omitempty"`
	LastOperation    *OperationResult         `json:"last_operation,omitempty"`
	LastAutoSwitch   *AutoSwitchStatus        `json:"last_auto_switch,omitempty"`
}

type AutoSwitchStatus struct {
	Decision      AutoSwitchDecision `json:"decision"`
	SSID          string             `json:"ssid,omitempty"`
	MatchedRuleID string             `json:"matched_rule_id,omitempty"`
	MatchedRule   string             `json:"matched_rule,omitempty"`
	Attempted     bool               `json:"attempted"`
	Success       bool               `json:"success"`
	Message       string             `json:"message"`
	MessageKey    string             `json:"message_key,omitempty"`
	CheckedAt     time.Time          `json:"checked_at"`
}

type ConfigurationComparison struct {
	Comparable  bool                      `json:"comparable"`
	Matches     bool                      `json:"matches"`
	NeedsApply  bool                      `json:"needs_apply"`
	Differences []ConfigurationDifference `json:"differences"`
	Message     string                    `json:"message,omitempty"`
	MessageKey  string                    `json:"message_key,omitempty"`
}

type ConfigurationDifference struct {
	Field   string `json:"field"`
	Current string `json:"current"`
	Target  string `json:"target"`
}

type OperationResult struct {
	Action            OperationAction          `json:"action"`
	Trigger           OperationTrigger         `json:"trigger"`
	RuleID            string                   `json:"rule_id,omitempty"`
	RuleName          string                   `json:"rule_name,omitempty"`
	Success           bool                     `json:"success"`
	DryRun            bool                     `json:"dry_run"`
	Verified          bool                     `json:"verified"`
	RollbackAttempted bool                     `json:"rollback_attempted"`
	RollbackSucceeded bool                     `json:"rollback_succeeded"`
	Message           string                   `json:"message"`
	MessageKey        string                   `json:"message_key,omitempty"`
	Plan              *OperationPlan           `json:"plan,omitempty"`
	State             *State                   `json:"state,omitempty"`
	Comparison        *ConfigurationComparison `json:"comparison,omitempty"`
	CompletedAt       time.Time                `json:"completed_at"`
}

type OperationPlan struct {
	Action    OperationAction `json:"action"`
	Service   string          `json:"service"`
	Interface string          `json:"interface"`
	Commands  []CommandPlan   `json:"commands"`
}

type CommandPlan struct {
	Executable string   `json:"executable"`
	Arguments  []string `json:"arguments"`
}

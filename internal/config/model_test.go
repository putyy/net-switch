package config

import (
	"errors"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	configuration := Default()
	if err := configuration.Validate(); err != nil {
		t.Fatalf("默认配置无效: %v", err)
	}
	if !configuration.General.AutoSwitch || configuration.General.UnmatchedAction != UnmatchedDHCP || configuration.General.Language != LanguageChinese {
		t.Fatalf("默认设置不符合预期: %#v", configuration.General)
	}
}

func TestStaticRuleIsValid(t *testing.T) {
	configuration := Config{
		General: GeneralSettings{AutoSwitch: true, UnmatchedAction: UnmatchedKeep, Language: LanguageChinese},
		Rules: []Rule{{
			ID:      "company-wifi",
			Name:    "公司网络",
			SSID:    "LM-1-5G",
			Enabled: true,
			IPv4: IPv4Config{
				Mode:    IPv4Static,
				Address: "192.168.10.66",
				Netmask: "255.255.255.0",
				Gateway: "192.168.10.1",
				DNS:     []string{"192.168.10.1", "1.1.1.1"},
			},
		}},
	}
	if err := configuration.Validate(); err != nil {
		t.Fatalf("合法静态规则校验失败: %v", err)
	}
}

func TestValidationRejectsInvalidConfigurations(t *testing.T) {
	tests := []struct {
		name  string
		field string
		edit  func(*Config)
	}{
		{
			name:  "unknown unmatched action",
			field: "general.unmatched_action",
			edit: func(configuration *Config) {
				configuration.General.UnmatchedAction = "reset"
			},
		},
		{
			name:  "unknown language",
			field: "general.language",
			edit: func(configuration *Config) {
				configuration.General.Language = "fr"
			},
		},
		{
			name:  "invalid address",
			field: "rules[0].ipv4.address",
			edit: func(configuration *Config) {
				configuration.Rules[0].IPv4.Address = "300.1.1.1"
			},
		},
		{
			name:  "non contiguous netmask",
			field: "rules[0].ipv4.netmask",
			edit: func(configuration *Config) {
				configuration.Rules[0].IPv4.Netmask = "255.0.255.0"
			},
		},
		{
			name:  "gateway outside subnet",
			field: "rules[0].ipv4.gateway",
			edit: func(configuration *Config) {
				configuration.Rules[0].IPv4.Gateway = "192.168.11.1"
			},
		},
		{
			name:  "gateway equals address",
			field: "rules[0].ipv4.gateway",
			edit: func(configuration *Config) {
				configuration.Rules[0].IPv4.Gateway = configuration.Rules[0].IPv4.Address
			},
		},
		{
			name:  "dhcp with static field",
			field: "rules[0].ipv4",
			edit: func(configuration *Config) {
				configuration.Rules[0].IPv4.Mode = IPv4DHCP
			},
		},
		{
			name:  "duplicate enabled ssid",
			field: "rules[1].ssid",
			edit: func(configuration *Config) {
				duplicate := configuration.Rules[0]
				duplicate.ID = "company-wifi-2"
				configuration.Rules = append(configuration.Rules, duplicate)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration := validStaticConfiguration()
			test.edit(&configuration)
			err := configuration.Validate()
			if err == nil {
				t.Fatal("预期校验失败")
			}
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("预期 ValidationError，得到 %T: %v", err, err)
			}
			if validationErr.Field != test.field {
				t.Fatalf("错误字段为 %q，预期 %q", validationErr.Field, test.field)
			}
		})
	}
}

func validStaticConfiguration() Config {
	return Config{
		General: GeneralSettings{AutoSwitch: true, UnmatchedAction: UnmatchedKeep, Language: LanguageChinese},
		Rules: []Rule{{
			ID:      "company-wifi",
			Name:    "公司网络",
			SSID:    "LM-1-5G",
			Enabled: true,
			IPv4: IPv4Config{
				Mode:    IPv4Static,
				Address: "192.168.10.66",
				Netmask: "255.255.255.0",
				Gateway: "192.168.10.1",
				DNS:     []string{"192.168.10.1"},
			},
		}},
	}
}

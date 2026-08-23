package config

type UnmatchedAction string

type Language string

const (
	UnmatchedKeep UnmatchedAction = "keep"
	UnmatchedDHCP UnmatchedAction = "dhcp"

	LanguageChinese Language = "zh-CN"
	LanguageEnglish Language = "en"
)

type IPv4Mode string

const (
	IPv4DHCP   IPv4Mode = "dhcp"
	IPv4Static IPv4Mode = "static"
)

type Config struct {
	General GeneralSettings `toml:"general" json:"general"`
	Rules   []Rule          `toml:"rules,omitempty" json:"rules"`
}

type GeneralSettings struct {
	AutoSwitch      bool            `toml:"auto_switch" json:"auto_switch"`
	UnmatchedAction UnmatchedAction `toml:"unmatched_action" json:"unmatched_action"`
	Language        Language        `toml:"language,omitempty" json:"language"`
}

type Rule struct {
	ID      string     `toml:"id" json:"id"`
	Name    string     `toml:"name" json:"name"`
	SSID    string     `toml:"ssid" json:"ssid"`
	Enabled bool       `toml:"enabled" json:"enabled"`
	IPv4    IPv4Config `toml:"ipv4" json:"ipv4"`
}

type IPv4Config struct {
	Mode    IPv4Mode `toml:"mode" json:"mode"`
	Address string   `toml:"address,omitempty" json:"address,omitempty"`
	Netmask string   `toml:"netmask,omitempty" json:"netmask,omitempty"`
	Gateway string   `toml:"gateway,omitempty" json:"gateway,omitempty"`
	DNS     []string `toml:"dns,omitempty" json:"dns,omitempty"`
}

func Default() Config {
	return Config{
		General: GeneralSettings{
			AutoSwitch:      true,
			UnmatchedAction: UnmatchedDHCP,
			Language:        LanguageChinese,
		},
	}
}

func (c *Config) ApplyDefaults() {
	if c.General.Language == "" {
		c.General.Language = LanguageChinese
	}
}

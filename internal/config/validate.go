package config

import (
	"fmt"
	"net"
	"net/netip"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxRuleIDBytes   = 64
	maxRuleNameRunes = 100
	maxSSIDBytes     = 32
	maxDNSServers    = 8
)

var ruleIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func (c Config) Validate() error {
	if c.General.Language != "" && c.General.Language != LanguageChinese && c.General.Language != LanguageEnglish {
		return invalid("general.language", "must be zh-CN or en")
	}
	if c.General.UnmatchedAction != UnmatchedKeep && c.General.UnmatchedAction != UnmatchedDHCP {
		return invalid("general.unmatched_action", "must be keep or dhcp")
	}

	ids := make(map[string]struct{}, len(c.Rules))
	enabledSSIDs := make(map[string]int, len(c.Rules))
	for index, rule := range c.Rules {
		prefix := fmt.Sprintf("rules[%d]", index)
		if err := rule.validate(prefix); err != nil {
			return err
		}
		if _, exists := ids[rule.ID]; exists {
			return invalid(prefix+".id", "rule ID must be unique")
		}
		ids[rule.ID] = struct{}{}

		if !rule.Enabled {
			continue
		}
		if previousIndex, exists := enabledSSIDs[rule.SSID]; exists {
			return invalid(prefix+".ssid", fmt.Sprintf("uses the same SSID as enabled rule rules[%d]", previousIndex))
		}
		enabledSSIDs[rule.SSID] = index
	}
	return nil
}

// Validate checks an IPv4 target independently before a system network change.
func (c IPv4Config) Validate() error {
	return c.validate("ipv4")
}

func (r Rule) validate(prefix string) error {
	if r.ID == "" {
		return invalid(prefix+".id", "is required")
	}
	if len(r.ID) > maxRuleIDBytes || !ruleIDPattern.MatchString(r.ID) {
		return invalid(prefix+".id", "must start with a letter or number and contain only letters, numbers, underscores, or hyphens; maximum 64 bytes")
	}
	if strings.TrimSpace(r.Name) == "" {
		return invalid(prefix+".name", "is required")
	}
	if utf8.RuneCountInString(r.Name) > maxRuleNameRunes || containsControl(r.Name) {
		return invalid(prefix+".name", "must be at most 100 characters and contain no control characters")
	}
	if r.SSID == "" {
		return invalid(prefix+".ssid", "is required")
	}
	if len(r.SSID) > maxSSIDBytes || !utf8.ValidString(r.SSID) || containsControl(r.SSID) {
		return invalid(prefix+".ssid", "must be valid UTF-8, at most 32 bytes, and contain no control characters")
	}
	return r.IPv4.validate(prefix + ".ipv4")
}

func (c IPv4Config) validate(prefix string) error {
	switch c.Mode {
	case IPv4DHCP:
		if c.Address != "" || c.Netmask != "" || c.Gateway != "" || len(c.DNS) != 0 {
			return invalid(prefix, "DHCP mode cannot include a static address, subnet mask, gateway, or DNS servers")
		}
		return nil
	case IPv4Static:
		return c.validateStatic(prefix)
	default:
		return invalid(prefix+".mode", "must be dhcp or static")
	}
}

func (c IPv4Config) validateStatic(prefix string) error {
	address, err := parseIPv4(prefix+".address", c.Address, false)
	if err != nil {
		return err
	}
	mask, prefixLength, err := parseNetmask(prefix+".netmask", c.Netmask)
	if err != nil {
		return err
	}
	gateway, err := parseIPv4(prefix+".gateway", c.Gateway, false)
	if err != nil {
		return err
	}
	if !sameSubnet(address, gateway, mask) {
		return invalid(prefix+".gateway", "must be in the same subnet as the static IPv4 address")
	}
	if address == gateway {
		return invalid(prefix+".gateway", "must differ from the static IPv4 address")
	}
	if prefixLength <= 30 && isNetworkOrBroadcast(address, mask) {
		return invalid(prefix+".address", "cannot be a network or broadcast address")
	}
	if prefixLength <= 30 && isNetworkOrBroadcast(gateway, mask) {
		return invalid(prefix+".gateway", "cannot be a network or broadcast address")
	}
	if len(c.DNS) > maxDNSServers {
		return invalid(prefix+".dns", "cannot contain more than 8 DNS servers")
	}
	seenDNS := make(map[netip.Addr]struct{}, len(c.DNS))
	for index, value := range c.DNS {
		dns, parseErr := parseIPv4(fmt.Sprintf("%s.dns[%d]", prefix, index), value, true)
		if parseErr != nil {
			return parseErr
		}
		if _, exists := seenDNS[dns]; exists {
			return invalid(fmt.Sprintf("%s.dns[%d]", prefix, index), "DNS servers must be unique")
		}
		seenDNS[dns] = struct{}{}
	}
	return nil
}

func parseIPv4(field, value string, allowLoopback bool) (netip.Addr, error) {
	if value == "" {
		return netip.Addr{}, invalid(field, "is required")
	}
	address, err := netip.ParseAddr(value)
	if err != nil || !address.Is4() {
		return netip.Addr{}, invalid(field, "must be a valid IPv4 address")
	}
	if address.IsUnspecified() || address.IsMulticast() || (!allowLoopback && address.IsLoopback()) {
		return netip.Addr{}, invalid(field, "cannot be an unspecified, multicast, or loopback address")
	}
	return address, nil
}

func parseNetmask(field, value string) ([4]byte, int, error) {
	parsed := net.ParseIP(value).To4()
	if parsed == nil {
		return [4]byte{}, 0, invalid(field, "must be a dotted-decimal IPv4 subnet mask")
	}
	mask := net.IPMask(parsed)
	ones, bits := mask.Size()
	if bits != 32 || ones == 0 {
		return [4]byte{}, 0, invalid(field, "must be a contiguous, non-zero IPv4 subnet mask")
	}
	return [4]byte(parsed), ones, nil
}

func sameSubnet(left, right netip.Addr, mask [4]byte) bool {
	leftBytes := left.As4()
	rightBytes := right.As4()
	for index := range mask {
		if leftBytes[index]&mask[index] != rightBytes[index]&mask[index] {
			return false
		}
	}
	return true
}

func isNetworkOrBroadcast(address netip.Addr, mask [4]byte) bool {
	addressBytes := address.As4()
	isNetwork := true
	isBroadcast := true
	for index := range mask {
		host := addressBytes[index] &^ mask[index]
		if host != 0 {
			isNetwork = false
		}
		if host != ^mask[index] {
			isBroadcast = false
		}
	}
	return isNetwork || isBroadcast
}

func containsControl(value string) bool {
	return strings.ContainsFunc(value, unicode.IsControl)
}

func invalid(field, message string) error {
	return &ValidationError{Field: field, Message: message}
}

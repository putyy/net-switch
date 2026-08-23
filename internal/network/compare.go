package network

import (
	"net/netip"
	"strings"

	"github.com/putyy/net-switch/internal/config"
)

// CompareIPv4 compares a readable system state with a validated rule target.
// NeedsApply is true only when the state is complete and a difference is known.
func CompareIPv4(current State, target config.IPv4Config) ConfigurationComparison {
	comparison := ConfigurationComparison{Differences: []ConfigurationDifference{}}
	if current.Status != StateStatusConnected {
		return comparisonUnavailable(comparison, "compare.disconnected", "The current network is disconnected, so the target configuration cannot be compared")
	}
	if current.Mode != AddressModeDHCP && current.Mode != AddressModeStatic {
		return comparisonUnavailable(comparison, "compare.mode_unknown", "The current IPv4 configuration mode is unknown")
	}
	if current.DNSMode != DNSModeAutomatic && current.DNSMode != DNSModeManual {
		return comparisonUnavailable(comparison, "compare.dns_unknown", "The current DNS configuration mode is unknown")
	}

	switch target.Mode {
	case config.IPv4DHCP:
		appendDifference(&comparison, "ipv4.mode", string(current.Mode), string(AddressModeDHCP))
		appendDifference(&comparison, "ipv4.dns_mode", string(current.DNSMode), string(DNSModeAutomatic))
	case config.IPv4Static:
		if current.Mode != AddressModeStatic {
			appendDifference(&comparison, "ipv4.mode", string(current.Mode), string(AddressModeStatic))
		} else if key, message := compareStaticFields(&comparison, current, target); message != "" {
			return comparisonUnavailable(comparison, key, message)
		}
		if key, message := compareDNS(&comparison, current, target.DNS); message != "" {
			return comparisonUnavailable(comparison, key, message)
		}
	default:
		return comparisonUnavailable(comparison, "compare.target_invalid", "The target IPv4 configuration mode is invalid")
	}

	comparison.Comparable = true
	comparison.Matches = len(comparison.Differences) == 0
	comparison.NeedsApply = !comparison.Matches
	if comparison.Matches {
		comparison.Message = "The current configuration matches the target rule"
		comparison.MessageKey = "compare.matches"
	} else {
		comparison.Message = "The current configuration differs from the target rule"
		comparison.MessageKey = "compare.differs"
	}
	return comparison
}

func compareStaticFields(comparison *ConfigurationComparison, current State, target config.IPv4Config) (string, string) {
	currentAddress, ok := canonicalIPv4(current.IPv4Address)
	if !ok {
		return "compare.current_address_invalid", "The current IPv4 address is missing or invalid"
	}
	currentNetmask, ok := canonicalIPv4(current.Netmask)
	if !ok {
		return "compare.current_netmask_invalid", "The current subnet mask is missing or invalid"
	}
	currentGateway, ok := canonicalIPv4(current.Gateway)
	if !ok {
		return "compare.current_gateway_invalid", "The current default gateway is missing or invalid"
	}
	targetAddress, ok := canonicalIPv4(target.Address)
	if !ok {
		return "compare.target_address_invalid", "The target IPv4 address is invalid"
	}
	targetNetmask, ok := canonicalIPv4(target.Netmask)
	if !ok {
		return "compare.target_netmask_invalid", "The target subnet mask is invalid"
	}
	targetGateway, ok := canonicalIPv4(target.Gateway)
	if !ok {
		return "compare.target_gateway_invalid", "The target default gateway is invalid"
	}

	appendDifference(comparison, "ipv4.address", currentAddress, targetAddress)
	appendDifference(comparison, "ipv4.netmask", currentNetmask, targetNetmask)
	appendDifference(comparison, "ipv4.gateway", currentGateway, targetGateway)
	return "", ""
}

func compareDNS(comparison *ConfigurationComparison, current State, target []string) (string, string) {
	targetMode := DNSModeAutomatic
	if len(target) > 0 {
		targetMode = DNSModeManual
	}
	appendDifference(comparison, "ipv4.dns_mode", string(current.DNSMode), string(targetMode))
	if current.DNSMode != DNSModeManual || targetMode != DNSModeManual {
		return "", ""
	}

	currentDNS, ok := canonicalAddresses(current.DNS)
	if !ok || len(currentDNS) == 0 {
		return "compare.current_dns_invalid", "The current manual DNS configuration is missing or invalid"
	}
	targetDNS, ok := canonicalAddresses(target)
	if !ok {
		return "compare.target_dns_invalid", "The target DNS configuration is invalid"
	}
	appendDifference(comparison, "ipv4.dns", strings.Join(currentDNS, ","), strings.Join(targetDNS, ","))
	return "", ""
}

func canonicalIPv4(value string) (string, bool) {
	address, err := netip.ParseAddr(value)
	if err != nil || !address.Is4() {
		return "", false
	}
	return address.Unmap().String(), true
}

func canonicalAddresses(values []string) ([]string, bool) {
	result := make([]string, len(values))
	for index, value := range values {
		address, err := netip.ParseAddr(value)
		if err != nil {
			return nil, false
		}
		result[index] = address.Unmap().String()
	}
	return result, true
}

func appendDifference(comparison *ConfigurationComparison, field, current, target string) {
	if current == target {
		return
	}
	comparison.Differences = append(comparison.Differences, ConfigurationDifference{
		Field:   field,
		Current: current,
		Target:  target,
	})
}

func comparisonUnavailable(comparison ConfigurationComparison, key, message string) ConfigurationComparison {
	comparison.Differences = []ConfigurationDifference{}
	comparison.Message = message
	comparison.MessageKey = key
	return comparison
}

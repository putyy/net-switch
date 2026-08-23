package network

import (
	"testing"

	"github.com/putyy/net-switch/internal/config"
)

func TestCompareIPv4MatchesDHCPWithAutomaticDNS(t *testing.T) {
	comparison := CompareIPv4(State{
		Status:  StateStatusConnected,
		Mode:    AddressModeDHCP,
		DNSMode: DNSModeAutomatic,
		DNS:     []string{"192.168.10.1"},
	}, config.IPv4Config{Mode: config.IPv4DHCP})

	if !comparison.Comparable || !comparison.Matches || comparison.NeedsApply || len(comparison.Differences) != 0 {
		t.Fatalf("DHCP 配置比较错误: %#v", comparison)
	}
}

func TestCompareIPv4DetectsManualDNSOnDHCP(t *testing.T) {
	comparison := CompareIPv4(State{
		Status:  StateStatusConnected,
		Mode:    AddressModeDHCP,
		DNSMode: DNSModeManual,
		DNS:     []string{"1.1.1.1"},
	}, config.IPv4Config{Mode: config.IPv4DHCP})

	if !comparison.Comparable || comparison.Matches || !comparison.NeedsApply || len(comparison.Differences) != 1 {
		t.Fatalf("未识别 DHCP 下的手动 DNS: %#v", comparison)
	}
}

func TestCompareIPv4MatchesStaticConfiguration(t *testing.T) {
	target := config.IPv4Config{
		Mode:    config.IPv4Static,
		Address: "192.168.10.66",
		Netmask: "255.255.255.0",
		Gateway: "192.168.10.1",
		DNS:     []string{"1.1.1.1", "8.8.8.8"},
	}
	comparison := CompareIPv4(State{
		Status:      StateStatusConnected,
		Mode:        AddressModeStatic,
		DNSMode:     DNSModeManual,
		IPv4Address: "192.168.10.66",
		Netmask:     "255.255.255.0",
		Gateway:     "192.168.10.1",
		DNS:         []string{"1.1.1.1", "8.8.8.8"},
	}, target)

	if !comparison.Comparable || !comparison.Matches || comparison.NeedsApply {
		t.Fatalf("静态配置比较错误: %#v", comparison)
	}
}

func TestCompareIPv4TreatsDNSOrderAsConfigurationDifference(t *testing.T) {
	target := config.IPv4Config{
		Mode:    config.IPv4Static,
		Address: "192.168.10.66",
		Netmask: "255.255.255.0",
		Gateway: "192.168.10.1",
		DNS:     []string{"1.1.1.1", "8.8.8.8"},
	}
	comparison := CompareIPv4(State{
		Status:      StateStatusConnected,
		Mode:        AddressModeStatic,
		DNSMode:     DNSModeManual,
		IPv4Address: target.Address,
		Netmask:     target.Netmask,
		Gateway:     target.Gateway,
		DNS:         []string{"8.8.8.8", "1.1.1.1"},
	}, target)

	if !comparison.NeedsApply || len(comparison.Differences) != 1 || comparison.Differences[0].Field != "ipv4.dns" {
		t.Fatalf("未识别 DNS 顺序差异: %#v", comparison)
	}
}

func TestCompareIPv4DoesNotApplyWhenCurrentStateIsIncomplete(t *testing.T) {
	comparison := CompareIPv4(State{
		Status: StateStatusConnected,
		Mode:   AddressModeDHCP,
	}, config.IPv4Config{Mode: config.IPv4DHCP})

	if comparison.Comparable || comparison.Matches || comparison.NeedsApply || comparison.Message == "" {
		t.Fatalf("信息不足时不应要求应用配置: %#v", comparison)
	}
}

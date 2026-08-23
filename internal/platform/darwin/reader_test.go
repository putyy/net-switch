//go:build darwin

package darwin

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/putyy/net-switch/internal/network"
)

const serviceOrderFixture = `An asterisk (*) denotes that a network service is disabled.
(1) USB 10/100/1000 LAN
(Hardware Port: USB 10/100/1000 LAN, Device: en5)

(2) Wi-Fi
(Hardware Port: Wi-Fi, Device: en0)

(*) Disabled Ethernet
(Hardware Port: Ethernet, Device: en9)
`

const routeFixture = `   route to: default
destination: default
       mask: default
    gateway: 192.168.10.1
  interface: en0
      flags: <UP,GATEWAY,DONE,STATIC,PRCLONING,GLOBAL>
`

const netstatRouteFixture = `Routing tables

Internet:
Destination        Gateway            Flags               Netif Expire
default            192.168.10.1       UGScg                 en0
127                127.0.0.1          UCS                   lo0
`

const dhcpInfoFixture = `DHCP Configuration
IP address: 192.168.10.66
Subnet mask: 255.255.255.0
Router: 192.168.10.1
Client ID:
IPv6: Automatic
`

const effectiveDNSFixture = `DNS configuration

resolver #1
  nameserver[0] : 192.168.10.1
  if_index : 15 (en0)
  flags    : Request A records

resolver #2
  nameserver[0] : 10.10.0.1
  if_index : 20 (en5)
  flags    : Request A records

DNS configuration (for scoped queries)

resolver #1
  nameserver[0] : 192.168.10.1
  nameserver[1] : 2001:4860:4860::8888
  if_index : 15 (en0)
  flags    : Scoped, Request A records
`

type fakeRunner struct {
	outputs map[string]string
	errors  map[string]error
}

func (r fakeRunner) Run(_ context.Context, path string, arguments ...string) (string, error) {
	key := commandKey(path, arguments...)
	if err := r.errors[key]; err != nil {
		return "", err
	}
	output, ok := r.outputs[key]
	if !ok {
		return "", fmt.Errorf("未定义命令: %s", key)
	}
	return output, nil
}

func TestParseServiceOrder(t *testing.T) {
	services, err := parseServiceOrder(serviceOrderFixture)
	if err != nil {
		t.Fatalf("解析服务顺序失败: %v", err)
	}
	if len(services) != 3 {
		t.Fatalf("服务数量 = %d，期望 3", len(services))
	}
	if services[1].Name != "Wi-Fi" || services[1].Interface != "en0" || services[1].HardwarePort != "Wi-Fi" {
		t.Fatalf("Wi-Fi 服务解析错误: %#v", services[1])
	}
	if !services[2].Disabled {
		t.Fatalf("未识别停用服务: %#v", services[2])
	}
}

func TestParseDefaultRoute(t *testing.T) {
	interfaceName, gateway, err := parseDefaultRoute(routeFixture)
	if err != nil {
		t.Fatalf("解析默认路由失败: %v", err)
	}
	if interfaceName != "en0" || gateway != "192.168.10.1" {
		t.Fatalf("默认路由错误: interface=%q gateway=%q", interfaceName, gateway)
	}
}

func TestParseNetstatDefaultRoute(t *testing.T) {
	interfaceName, gateway, found := parseNetstatDefaultRoute(netstatRouteFixture)
	if !found || interfaceName != "en0" || gateway != "192.168.10.1" {
		t.Fatalf("netstat 默认路由错误: found=%t interface=%q gateway=%q", found, interfaceName, gateway)
	}
}

func TestApplyNetworkInfo(t *testing.T) {
	state := network.State{Mode: network.AddressModeUnknown}
	applyNetworkInfo(&state, dhcpInfoFixture)
	if state.Mode != network.AddressModeDHCP || state.IPv4Address != "192.168.10.66" || state.Netmask != "255.255.255.0" || state.Gateway != "192.168.10.1" {
		t.Fatalf("DHCP 信息解析错误: %#v", state)
	}

	applyNetworkInfo(&state, strings.Replace(dhcpInfoFixture, "DHCP Configuration", "Manual Configuration", 1))
	if state.Mode != network.AddressModeStatic {
		t.Fatalf("静态模式解析错误: %#v", state)
	}
}

func TestParseDNS(t *testing.T) {
	wanted := []string{"192.168.10.1", "2001:4860:4860::8888"}
	got := parseDNS("192.168.10.1\n2001:4860:4860::8888\n192.168.10.1\n")
	if !reflect.DeepEqual(got, wanted) {
		t.Fatalf("DNS = %#v，期望 %#v", got, wanted)
	}
	servers, mode := parseDNSConfiguration("There aren't any DNS Servers set on Wi-Fi.\n")
	if len(servers) != 0 || mode != network.DNSModeAutomatic {
		t.Fatalf("自动 DNS 解析错误: %#v, %q", servers, mode)
	}
}

func TestParseEffectiveDNSForInterface(t *testing.T) {
	wanted := []string{"192.168.10.1", "2001:4860:4860::8888"}
	got := parseEffectiveDNS(effectiveDNSFixture, "en0")
	if !reflect.DeepEqual(got, wanted) {
		t.Fatalf("有效 DNS = %#v，期望 %#v", got, wanted)
	}
}

func TestReaderCombinesCurrentWiFiState(t *testing.T) {
	runner := fakeRunner{outputs: map[string]string{
		commandKey(networkSetupPath, "-listnetworkserviceorder"):  serviceOrderFixture,
		commandKey(routePath, "-n", "get", "default"):             routeFixture,
		commandKey(networkSetupPath, "-getinfo", "Wi-Fi"):         dhcpInfoFixture,
		commandKey(networkSetupPath, "-getdnsservers", "Wi-Fi"):   "192.168.10.1\n1.1.1.1\n",
		commandKey(networkSetupPath, "-getairportnetwork", "en0"): "Current Wi-Fi Network: Office:5G\n",
	}}

	state, err := newReader(runner).Read(context.Background())
	if err != nil {
		t.Fatalf("读取状态失败: %v", err)
	}
	if state.Status != network.StateStatusConnected || state.Service != "Wi-Fi" || state.Interface != "en0" || state.SSID != "Office:5G" {
		t.Fatalf("网络状态错误: %#v", state)
	}
	if !reflect.DeepEqual(state.DNS, []string{"192.168.10.1", "1.1.1.1"}) {
		t.Fatalf("DNS 错误: %#v", state.DNS)
	}
	if state.DNSMode != network.DNSModeManual {
		t.Fatalf("DNS 模式错误: %q", state.DNSMode)
	}
}

func TestReaderReturnsDisconnectedStateWithoutDefaultRoute(t *testing.T) {
	runner := fakeRunner{
		outputs: map[string]string{
			commandKey(networkSetupPath, "-listnetworkserviceorder"): serviceOrderFixture,
			commandKey(netstatPath, "-rn", "-f", "inet"):             "Routing tables\n\nInternet:\nDestination Gateway Flags Netif\n",
		},
		errors: map[string]error{
			commandKey(routePath, "-n", "get", "default"): errors.New("not in table"),
		},
	}

	state, err := newReader(runner).Read(context.Background())
	if err != nil {
		t.Fatalf("断网状态不应作为读取错误: %v", err)
	}
	if state.Status != network.StateStatusDisconnected || state.Service != "Wi-Fi" || state.Interface != "en0" {
		t.Fatalf("断网状态错误: %#v", state)
	}
}

func TestReaderFallsBackToNetstatDefaultRoute(t *testing.T) {
	runner := fakeRunner{
		outputs: map[string]string{
			commandKey(networkSetupPath, "-listnetworkserviceorder"):  serviceOrderFixture,
			commandKey(netstatPath, "-rn", "-f", "inet"):              netstatRouteFixture,
			commandKey(networkSetupPath, "-getinfo", "Wi-Fi"):         dhcpInfoFixture,
			commandKey(networkSetupPath, "-getdnsservers", "Wi-Fi"):   "192.168.10.1\n",
			commandKey(networkSetupPath, "-getairportnetwork", "en0"): "Current Wi-Fi Network: Office-WiFi\n",
		},
		errors: map[string]error{
			commandKey(routePath, "-n", "get", "default"): errors.New("not in table"),
		},
	}

	state, err := newReader(runner).Read(context.Background())
	if err != nil {
		t.Fatalf("netstat 回退读取失败: %v", err)
	}
	if state.Status != network.StateStatusConnected || state.Interface != "en0" || state.Gateway != "192.168.10.1" {
		t.Fatalf("netstat 回退网络状态错误: %#v", state)
	}
}

func TestReaderUsesEffectiveDNSWhenServiceUsesAutomaticDNS(t *testing.T) {
	runner := fakeRunner{outputs: map[string]string{
		commandKey(networkSetupPath, "-listnetworkserviceorder"):  serviceOrderFixture,
		commandKey(routePath, "-n", "get", "default"):             routeFixture,
		commandKey(networkSetupPath, "-getinfo", "Wi-Fi"):         dhcpInfoFixture,
		commandKey(networkSetupPath, "-getdnsservers", "Wi-Fi"):   "There aren't any DNS Servers set on Wi-Fi.\n",
		commandKey(scutilPath, "--dns"):                           effectiveDNSFixture,
		commandKey(networkSetupPath, "-getairportnetwork", "en0"): "Current Wi-Fi Network: Office-WiFi\n",
	}}

	state, err := newReader(runner).Read(context.Background())
	if err != nil {
		t.Fatalf("读取状态失败: %v", err)
	}
	if !reflect.DeepEqual(state.DNS, []string{"192.168.10.1", "2001:4860:4860::8888"}) {
		t.Fatalf("未读取到有效 DNS: %#v", state.DNS)
	}
	if state.DNSMode != network.DNSModeAutomatic {
		t.Fatalf("未识别自动 DNS: %q", state.DNSMode)
	}
}

func commandKey(path string, arguments ...string) string {
	return strings.Join(append([]string{path}, arguments...), "\x00")
}

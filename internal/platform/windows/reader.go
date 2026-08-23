//go:build windows

package windows

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"syscall"

	"github.com/putyy/net-switch/internal/network"
)

const powershellPath = "powershell.exe"

const networkStateScript = `$ErrorActionPreference='Stop'; $OutputEncoding=[Console]::OutputEncoding=[System.Text.Encoding]::UTF8; $all=@(Get-NetIPConfiguration | Where-Object { $_.NetAdapter.Status -eq 'Up' -and $_.IPv4Address }); $cfg=$all | Where-Object { $_.NetAdapter.NdisPhysicalMedium -match '802.11|Wireless' -or $_.NetAdapter.PhysicalMediaType -match '802.11|Wireless' } | Select-Object -First 1; $wifi=$null -ne $cfg; if ($null -eq $cfg) { $cfg=$all | Where-Object { $_.IPv4DefaultGateway } | Select-Object -First 1 }; if ($null -eq $cfg) { Write-Output 'null'; exit 0 }; $profile=Get-NetConnectionProfile -InterfaceIndex $cfg.InterfaceIndex -ErrorAction SilentlyContinue; $ip=$cfg.IPv4Address | Select-Object -First 1; $iface=Get-NetIPInterface -InterfaceIndex $cfg.InterfaceIndex -AddressFamily IPv4 | Select-Object -First 1; $adapter=Get-NetAdapter -InterfaceIndex $cfg.InterfaceIndex | Select-Object -First 1; $dns=@((Get-DnsClientServerAddress -InterfaceIndex $cfg.InterfaceIndex -AddressFamily IPv4).ServerAddresses); $manualDNS=$false; if ($adapter) { $registryPath='HKLM:\SYSTEM\CurrentControlSet\Services\Tcpip\Parameters\Interfaces\{'+[string]$adapter.InterfaceGuid+'}'; $configured=[string](Get-ItemProperty -LiteralPath $registryPath -Name NameServer -ErrorAction SilentlyContinue).NameServer; $manualDNS=-not [string]::IsNullOrWhiteSpace($configured) }; [pscustomobject]@{ IsWiFi=$wifi; SSID=$(if ($wifi -and $profile) {$profile.Name} else {''}); Service=$cfg.InterfaceAlias; Interface=[string]$cfg.InterfaceIndex; Address=$ip.IPAddress; PrefixLength=$ip.PrefixLength; Gateway=$(if ($cfg.IPv4DefaultGateway) {$cfg.IPv4DefaultGateway.NextHop} else {''}); DNS=$dns; DHCP=[string]$iface.Dhcp; DNSManual=$manualDNS } | ConvertTo-Json -Compress`

type commandRunner interface {
	Run(context.Context, string, ...string) (string, error)
}

type systemCommandRunner struct{}

func (systemCommandRunner) Run(ctx context.Context, path string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, path, arguments...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("execute %s: %s: %w", path, strings.TrimSpace(string(output)), err)
	}
	return string(output), nil
}

type Reader struct {
	runner commandRunner
}

type statePayload struct {
	IsWiFi       bool     `json:"IsWiFi"`
	SSID         string   `json:"SSID"`
	Service      string   `json:"Service"`
	Interface    string   `json:"Interface"`
	Address      string   `json:"Address"`
	PrefixLength int      `json:"PrefixLength"`
	Gateway      string   `json:"Gateway"`
	DNS          []string `json:"DNS"`
	DHCP         string   `json:"DHCP"`
	DNSManual    bool     `json:"DNSManual"`
}

func NewReader() *Reader {
	return &Reader{runner: systemCommandRunner{}}
}

func (r *Reader) Read(ctx context.Context) (network.State, error) {
	return readState(ctx, r.runner)
}

func readState(ctx context.Context, runner commandRunner) (network.State, error) {
	state := network.State{
		Status:  network.StateStatusUnavailable,
		Mode:    network.AddressModeUnknown,
		DNSMode: network.DNSModeUnknown,
		DNS:     []string{},
	}
	output, err := runner.Run(ctx, powershellPath, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", networkStateScript)
	if err != nil {
		state.Message = "Could not read Windows network status"
		state.MessageKey = "state.windows_failed"
		return state, err
	}
	output = strings.TrimPrefix(output, "\ufeff")
	if strings.TrimSpace(output) == "null" || strings.TrimSpace(output) == "" {
		state.Status = network.StateStatusDisconnected
		state.Message = "No IPv4 network connection is available"
		state.MessageKey = "state.disconnected"
		return state, nil
	}
	var payload statePayload
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		state.Message = "Windows returned an unrecognized network status"
		state.MessageKey = "state.windows_invalid"
		return state, fmt.Errorf("parse PowerShell network status: %w", err)
	}
	state.Status = network.StateStatusConnected
	state.Service = strings.TrimSpace(payload.Service)
	state.Interface = strings.TrimSpace(payload.Interface)
	state.IPv4Address = validIPv4(payload.Address)
	state.Gateway = validIPv4(payload.Gateway)
	state.Netmask = prefixMask(payload.PrefixLength)
	for _, server := range payload.DNS {
		if address := validIPv4(server); address != "" {
			state.DNS = appendUnique(state.DNS, address)
		}
	}
	if strings.EqualFold(payload.DHCP, "Enabled") {
		state.Mode = network.AddressModeDHCP
	} else if strings.EqualFold(payload.DHCP, "Disabled") {
		state.Mode = network.AddressModeStatic
	}
	if payload.DNSManual {
		state.DNSMode = network.DNSModeManual
	} else {
		state.DNSMode = network.DNSModeAutomatic
	}
	if payload.IsWiFi {
		state.SSID = strings.TrimSpace(payload.SSID)
		if state.SSID == "" {
			state.Message = "Wi-Fi is connected, but Windows did not return its name"
			state.MessageKey = "state.windows_ssid_missing"
		}
	} else {
		state.Message = "The active network is not Wi-Fi"
		state.MessageKey = "state.non_wifi"
	}
	return state, nil
}

func prefixMask(prefix int) string {
	if prefix < 0 || prefix > 32 {
		return ""
	}
	return net.IP(net.CIDRMask(prefix, 32)).String()
}

func validIPv4(value string) string {
	parsed := net.ParseIP(strings.TrimSpace(value))
	if parsed == nil || parsed.To4() == nil {
		return ""
	}
	return parsed.String()
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

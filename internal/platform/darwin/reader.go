//go:build darwin

package darwin

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strings"

	"github.com/putyy/net-switch/internal/network"
)

const (
	networkSetupPath = "/usr/sbin/networksetup"
	routePath        = "/sbin/route"
	netstatPath      = "/usr/sbin/netstat"
	scutilPath       = "/usr/sbin/scutil"
)

var (
	serviceLinePattern  = regexp.MustCompile(`^\((\d+|\*)\)\s+(.+)$`)
	hardwareLinePattern = regexp.MustCompile(`^\(Hardware Port:\s*(.+),\s*Device:\s*([^)]+)\)$`)
)

type commandRunner interface {
	Run(context.Context, string, ...string) (string, error)
}

type ssidAccess int

const (
	ssidAccessAvailable ssidAccess = iota
	ssidAccessPending
	ssidAccessDenied
	ssidAccessRestricted
	ssidAccessUnavailable
)

type ssidProvider interface {
	CurrentSSID(string) (string, ssidAccess, error)
}

type systemCommandRunner struct{}

func (systemCommandRunner) Run(ctx context.Context, path string, arguments ...string) (string, error) {
	output, err := exec.CommandContext(ctx, path, arguments...).CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		if detail != "" {
			return string(output), fmt.Errorf("execute %s: %s: %w", path, detail, err)
		}
		return string(output), fmt.Errorf("execute %s: %w", path, err)
	}
	return string(output), nil
}

type service struct {
	Name         string
	HardwarePort string
	Interface    string
	Disabled     bool
}

type Reader struct {
	runner       commandRunner
	ssidProvider ssidProvider
}

func NewReader() *Reader {
	return &Reader{runner: systemCommandRunner{}, ssidProvider: nativeSSIDProvider{}}
}

func newReader(runner commandRunner) *Reader {
	return &Reader{runner: runner}
}

func (r *Reader) Read(ctx context.Context) (network.State, error) {
	state := network.State{
		Status:  network.StateStatusUnavailable,
		Mode:    network.AddressModeUnknown,
		DNSMode: network.DNSModeUnknown,
		DNS:     []string{},
	}

	serviceOutput, err := r.runner.Run(ctx, networkSetupPath, "-listnetworkserviceorder")
	if err != nil {
		state.Message = "Could not read macOS network services"
		state.MessageKey = "state.services_failed"
		return state, err
	}
	services, err := parseServiceOrder(serviceOutput)
	if err != nil {
		state.Message = "macOS did not return any available network services"
		state.MessageKey = "state.no_services"
		return state, err
	}

	interfaceName, gateway, connected, err := r.readDefaultRoute(ctx)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			state.Message = "Reading network status timed out"
			state.MessageKey = "state.timeout"
			return state, ctx.Err()
		}
		state.Message = "The default macOS network interface could not be identified; retrying shortly"
		state.MessageKey = "state.route_unknown"
		return state, err
	}
	if !connected {
		if wifi, ok := firstWiFiService(services); ok {
			state.Service = wifi.Name
			state.Interface = wifi.Interface
		}
		state.Status = network.StateStatusDisconnected
		state.Message = "No default IPv4 network is available"
		state.MessageKey = "state.no_default_route"
		return state, nil
	}
	state.Interface = interfaceName
	state.Gateway = gateway
	state.Status = network.StateStatusConnected

	activeService, ok := serviceForInterface(services, interfaceName)
	if !ok {
		state.Message = "The network service for the default interface could not be identified"
		state.MessageKey = "state.service_unknown"
		return state, fmt.Errorf("no network service found for interface %q", interfaceName)
	}
	state.Service = activeService.Name

	infoOutput, err := r.runner.Run(ctx, networkSetupPath, "-getinfo", activeService.Name)
	if err != nil {
		state.Message = "The current IPv4 configuration could not be read"
		state.MessageKey = "state.ipv4_failed"
		return state, err
	}
	applyNetworkInfo(&state, infoOutput)

	dnsOutput, err := r.runner.Run(ctx, networkSetupPath, "-getdnsservers", activeService.Name)
	if err != nil {
		state.Message = "The network is available, but DNS settings could not be read"
		state.MessageKey = "state.dns_failed"
		return state, err
	}
	state.DNS, state.DNSMode = parseDNSConfiguration(dnsOutput)
	if len(state.DNS) == 0 {
		effectiveDNSOutput, effectiveDNSError := r.runner.Run(ctx, scutilPath, "--dns")
		if effectiveDNSError == nil {
			state.DNS = parseEffectiveDNS(effectiveDNSOutput, activeService.Interface)
		}
	}

	if !isWiFi(activeService.HardwarePort) {
		state.Message = "The active network is not Wi-Fi"
		state.MessageKey = "state.non_wifi"
		return state, nil
	}

	if r.ssidProvider != nil {
		ssid, access, providerErr := r.ssidProvider.CurrentSSID(activeService.Interface)
		switch {
		case providerErr == nil && ssid != "":
			state.SSID = ssid
			return state, nil
		case access == ssidAccessPending:
			state.Message = "Waiting for macOS location permission"
			state.MessageKey = "state.permission_pending"
			return state, nil
		case access == ssidAccessDenied:
			state.Message = "Location permission was denied for Net Switch"
			state.MessageKey = "state.permission_denied"
			return state, nil
		case access == ssidAccessRestricted:
			state.Message = "Location services are restricted, so the current Wi-Fi name cannot be read"
			state.MessageKey = "state.permission_restricted"
			return state, nil
		}
	}

	ssidOutput, err := r.runner.Run(ctx, networkSetupPath, "-getairportnetwork", activeService.Interface)
	if err == nil {
		if ssid, ok := parseSSID(ssidOutput); ok {
			state.SSID = ssid
			return state, nil
		}
	}
	state.Message = "Wi-Fi is connected, but macOS did not return its name"
	state.MessageKey = "state.ssid_unavailable"
	return state, nil
}

func (r *Reader) readDefaultRoute(ctx context.Context) (string, string, bool, error) {
	routeOutput, routeErr := r.runner.Run(ctx, routePath, "-n", "get", "default")
	if routeErr == nil {
		interfaceName, gateway, parseErr := parseDefaultRoute(routeOutput)
		if parseErr == nil {
			return interfaceName, gateway, true, nil
		}
		netstatOutput, netstatErr := r.runner.Run(ctx, netstatPath, "-rn", "-f", "inet")
		if netstatErr != nil {
			return "", "", false, errors.Join(parseErr, netstatErr)
		}
		interfaceName, gateway, found := parseNetstatDefaultRoute(netstatOutput)
		if !found {
			return "", "", false, parseErr
		}
		return interfaceName, gateway, true, nil
	}

	netstatOutput, netstatErr := r.runner.Run(ctx, netstatPath, "-rn", "-f", "inet")
	if netstatErr != nil {
		return "", "", false, errors.Join(routeErr, netstatErr)
	}
	interfaceName, gateway, found := parseNetstatDefaultRoute(netstatOutput)
	if !found {
		return "", "", false, nil
	}
	return interfaceName, gateway, true, nil
}

func parseDNSConfiguration(output string) ([]string, network.DNSMode) {
	if strings.Contains(strings.ToLower(output), "dns servers set on") {
		return []string{}, network.DNSModeAutomatic
	}
	servers := parseDNS(output)
	if len(servers) == 0 {
		return servers, network.DNSModeUnknown
	}
	return servers, network.DNSModeManual
}

func parseServiceOrder(output string) ([]service, error) {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	services := make([]service, 0)
	var pending *service
	for _, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if matches := serviceLinePattern.FindStringSubmatch(line); len(matches) == 3 {
			name := strings.TrimSpace(matches[2])
			disabled := matches[1] == "*" || strings.HasPrefix(name, "*")
			name = strings.TrimSpace(strings.TrimPrefix(name, "*"))
			pending = &service{Name: name, Disabled: disabled}
			continue
		}
		if pending == nil {
			continue
		}
		matches := hardwareLinePattern.FindStringSubmatch(line)
		if len(matches) != 3 {
			continue
		}
		pending.HardwarePort = strings.TrimSpace(matches[1])
		pending.Interface = strings.TrimSpace(matches[2])
		if pending.Name != "" && pending.Interface != "" {
			services = append(services, *pending)
		}
		pending = nil
	}
	if len(services) == 0 {
		return nil, errors.New("network service list is empty")
	}
	return services, nil
}

func parseDefaultRoute(output string) (string, string, error) {
	values := parseLabelledLines(output)
	interfaceName := values["interface"]
	if interfaceName == "" {
		return "", "", errors.New("default route is missing an interface")
	}
	gateway := values["gateway"]
	if parsed := net.ParseIP(gateway); parsed == nil || parsed.To4() == nil {
		gateway = ""
	}
	return interfaceName, gateway, nil
}

func parseNetstatDefaultRoute(output string) (string, string, bool) {
	for _, rawLine := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		fields := strings.Fields(rawLine)
		if len(fields) < 4 || fields[0] != "default" {
			continue
		}
		return fields[3], validIPv4(fields[1]), true
	}
	return "", "", false
}

func applyNetworkInfo(state *network.State, output string) {
	lowerOutput := strings.ToLower(output)
	switch {
	case strings.Contains(lowerOutput, "manual configuration"):
		state.Mode = network.AddressModeStatic
	case strings.Contains(lowerOutput, "dhcp configuration"):
		state.Mode = network.AddressModeDHCP
	}

	values := parseLabelledLines(output)
	state.IPv4Address = validIPv4(values["ip address"])
	state.Netmask = validIPv4(values["subnet mask"])
	if gateway := validIPv4(values["router"]); gateway != "" {
		state.Gateway = gateway
	}
}

func parseDNS(output string) []string {
	servers := make([]string, 0)
	seen := make(map[string]struct{})
	for _, rawLine := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		candidate := strings.TrimSpace(rawLine)
		if net.ParseIP(candidate) == nil {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		servers = append(servers, candidate)
	}
	return servers
}

func parseEffectiveDNS(output, interfaceName string) []string {
	servers := make([]string, 0)
	seen := make(map[string]struct{})
	for _, block := range splitResolverBlocks(output) {
		values := parseLabelledLines(block)
		ifIndex := values["if_index"]
		if !strings.Contains(ifIndex, "("+interfaceName+")") {
			continue
		}
		for _, rawLine := range strings.Split(block, "\n") {
			label, value, found := strings.Cut(rawLine, ":")
			if !found || !strings.HasPrefix(strings.TrimSpace(label), "nameserver[") {
				continue
			}
			candidate := strings.TrimSpace(value)
			if net.ParseIP(candidate) == nil {
				continue
			}
			if _, exists := seen[candidate]; exists {
				continue
			}
			seen[candidate] = struct{}{}
			servers = append(servers, candidate)
		}
	}
	return servers
}

func splitResolverBlocks(output string) []string {
	normalized := strings.ReplaceAll(output, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	blocks := make([]string, 0)
	current := make([]string, 0)
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "resolver #") && len(current) > 0 {
			blocks = append(blocks, strings.Join(current, "\n"))
			current = current[:0]
		}
		if len(current) > 0 || strings.HasPrefix(strings.TrimSpace(line), "resolver #") {
			current = append(current, line)
		}
	}
	if len(current) > 0 {
		blocks = append(blocks, strings.Join(current, "\n"))
	}
	return blocks
}

func parseSSID(output string) (string, bool) {
	for _, rawLine := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		prefix, ssid, found := strings.Cut(strings.TrimSpace(rawLine), ":")
		if !found || !strings.Contains(strings.ToLower(prefix), "network") {
			continue
		}
		ssid = strings.TrimSpace(ssid)
		if ssid == "" || strings.EqualFold(ssid, "none") || strings.Contains(strings.ToLower(ssid), "not associated") {
			return "", false
		}
		return ssid, true
	}
	return "", false
}

func parseLabelledLines(output string) map[string]string {
	values := make(map[string]string)
	for _, rawLine := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		label, value, found := strings.Cut(rawLine, ":")
		if !found {
			continue
		}
		values[strings.ToLower(strings.TrimSpace(label))] = strings.TrimSpace(value)
	}
	return values
}

func validIPv4(value string) string {
	parsed := net.ParseIP(value)
	if parsed == nil || parsed.To4() == nil {
		return ""
	}
	return value
}

func serviceForInterface(services []service, interfaceName string) (service, bool) {
	for _, candidate := range services {
		if !candidate.Disabled && candidate.Interface == interfaceName {
			return candidate, true
		}
	}
	return service{}, false
}

func firstWiFiService(services []service) (service, bool) {
	for _, candidate := range services {
		if !candidate.Disabled && isWiFi(candidate.HardwarePort) {
			return candidate, true
		}
	}
	return service{}, false
}

func isWiFi(hardwarePort string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(hardwarePort, "-", ""))
	return strings.Contains(normalized, "wifi") || strings.Contains(normalized, "airport")
}

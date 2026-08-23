//go:build linux

package linux

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/putyy/net-switch/internal/network"
)

const nmcliPath = "nmcli"

type commandRunner interface {
	Run(context.Context, string, ...string) (string, error)
}

type systemCommandRunner struct{}

func (systemCommandRunner) Run(ctx context.Context, path string, arguments ...string) (string, error) {
	command := exec.CommandContext(ctx, path, arguments...)
	command.Env = append(os.Environ(), "LC_ALL=C")
	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("execute %s: %s: %w", path, strings.TrimSpace(string(output)), err)
	}
	return string(output), nil
}

type Reader struct {
	runner commandRunner
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

	statusOutput, err := runner.Run(ctx, nmcliPath, "-t", "-e", "yes", "-f", "DEVICE,TYPE,STATE,CONNECTION", "device", "status")
	if err != nil {
		state.Message = "Could not read NetworkManager status; make sure nmcli is installed and running"
		state.MessageKey = "state.linux_failed"
		return state, err
	}
	device, connection, ok := parseActiveWiFi(statusOutput)
	if !ok {
		state.Status = network.StateStatusDisconnected
		state.Message = "No Wi-Fi network is connected"
		state.MessageKey = "state.disconnected"
		return state, nil
	}
	state.Status = network.StateStatusConnected
	state.Interface = device
	state.Service = connection
	connectionID := connection
	connectionByUUID := false
	identityOutput, identityErr := runner.Run(ctx, nmcliPath, "-t", "-e", "yes", "-f", "GENERAL.CONNECTION,GENERAL.CON-UUID", "device", "show", device)
	if identityErr == nil {
		identity := parseConnectionIdentity(identityOutput)
		if identity.Name != "" {
			state.Service = identity.Name
		}
		if identity.UUID != "" {
			connectionID = identity.UUID
			connectionByUUID = true
		}
	}

	ssidOutput, ssidErr := runner.Run(ctx, nmcliPath, "-t", "-e", "yes", "-f", "IN-USE,SSID", "device", "wifi", "list", "ifname", device)
	if ssidErr == nil {
		state.SSID = parseActiveSSID(ssidOutput)
	}

	detailOutput, err := runner.Run(ctx, nmcliPath, "-t", "-e", "yes", "-f", "IP4.ADDRESS,IP4.GATEWAY,IP4.DNS", "device", "show", device)
	if err != nil {
		state.Message = "Wi-Fi is connected, but its IPv4 configuration could not be read"
		state.MessageKey = "state.ipv4_failed"
		return state, err
	}
	applyDetails(&state, detailOutput)
	profileArguments := []string{"-t", "-e", "yes", "-f", "ipv4.method,ipv4.dns,ipv4.ignore-auto-dns", "connection", "show"}
	if connectionByUUID {
		profileArguments = append(profileArguments, "uuid")
	}
	profileArguments = append(profileArguments, connectionID)
	profileOutput, profileErr := runner.Run(ctx, nmcliPath, profileArguments...)
	if profileErr == nil {
		applyProfile(&state, profileOutput)
	}
	if state.SSID == "" {
		state.Message = "Wi-Fi is connected, but NetworkManager did not return an SSID"
		state.MessageKey = "state.linux_ssid_missing"
	}
	return state, nil
}

func parseActiveWiFi(output string) (string, string, bool) {
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		fields := splitEscaped(line, ':')
		if len(fields) >= 4 && fields[1] == "wifi" && fields[2] == "connected" && fields[0] != "" {
			return fields[0], fields[3], true
		}
	}
	return "", "", false
}

func parseActiveSSID(output string) string {
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		fields := splitEscaped(line, ':')
		if len(fields) >= 2 && fields[0] == "*" {
			return strings.Join(fields[1:], ":")
		}
	}
	return ""
}

func applyDetails(state *network.State, output string) {
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		fields := splitEscaped(line, ':')
		if len(fields) < 2 {
			continue
		}
		key := fields[0]
		value := strings.Join(fields[1:], ":")
		switch {
		case strings.HasPrefix(key, "IP4.ADDRESS") && state.IPv4Address == "":
			address, mask := parseCIDR(value)
			state.IPv4Address, state.Netmask = address, mask
		case key == "IP4.GATEWAY":
			state.Gateway = validIPv4(value)
		case strings.HasPrefix(key, "IP4.DNS"):
			if dns := validIPv4(value); dns != "" {
				state.DNS = appendUnique(state.DNS, dns)
			}
		}
	}
}

type connectionIdentity struct {
	Name string
	UUID string
}

func parseConnectionIdentity(output string) connectionIdentity {
	properties := parseProperties(output)
	return connectionIdentity{
		Name: firstProperty(properties, "GENERAL.CONNECTION"),
		UUID: firstProperty(properties, "GENERAL.CON-UUID"),
	}
}

func applyProfile(state *network.State, output string) {
	properties := parseProperties(output)
	method := firstProperty(properties, "ipv4.method")
	switch method {
	case "auto":
		state.Mode = network.AddressModeDHCP
	case "manual":
		state.Mode = network.AddressModeStatic
	}

	manualDNS := strings.EqualFold(firstProperty(properties, "ipv4.ignore-auto-dns"), "yes")
	for _, configured := range properties["ipv4.dns"] {
		if strings.TrimSpace(configured) != "" && strings.TrimSpace(configured) != "--" {
			manualDNS = true
			break
		}
	}
	if manualDNS {
		state.DNSMode = network.DNSModeManual
	} else {
		state.DNSMode = network.DNSModeAutomatic
	}
}

func parseProperties(output string) map[string][]string {
	properties := make(map[string][]string)
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		fields := splitEscaped(line, ':')
		if len(fields) < 2 || fields[0] == "" {
			continue
		}
		properties[fields[0]] = append(properties[fields[0]], strings.Join(fields[1:], ":"))
	}
	return properties
}

func firstProperty(properties map[string][]string, key string) string {
	values := properties[key]
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func parseCIDR(value string) (string, string) {
	address, networkBlock, err := net.ParseCIDR(value)
	if err != nil || address.To4() == nil {
		return "", ""
	}
	ones, bits := networkBlock.Mask.Size()
	if bits != 32 || ones < 0 {
		return "", ""
	}
	mask := net.CIDRMask(ones, bits)
	return address.String(), net.IP(mask).String()
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

func splitEscaped(value string, separator rune) []string {
	if value == "" {
		return nil
	}
	fields := make([]string, 1)
	escaped := false
	for _, character := range value {
		switch {
		case escaped:
			fields[len(fields)-1] += string(character)
			escaped = false
		case character == '\\':
			escaped = true
		case character == separator:
			fields = append(fields, "")
		default:
			fields[len(fields)-1] += string(character)
		}
	}
	if escaped {
		fields[len(fields)-1] += "\\"
	}
	return fields
}

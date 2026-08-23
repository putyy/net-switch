//go:build windows

package windows

import (
	"context"
	"strings"
	"testing"

	"github.com/putyy/net-switch/internal/config"
	"github.com/putyy/net-switch/internal/network"
)

const windowsDHCPStateFixture = `{"IsWiFi":true,"SSID":"LM-1","Service":"Wi-Fi","Interface":"12","Address":"192.168.10.50","PrefixLength":24,"Gateway":"192.168.10.1","DNS":["192.168.10.1"],"DHCP":"Enabled","DNSManual":false}`

type windowsRecordedCommand struct {
	path      string
	arguments []string
}

type windowsRecordingRunner struct {
	outputs       map[string]string
	errors        map[string]error
	defaultOutput string
	calls         []windowsRecordedCommand
}

func (r *windowsRecordingRunner) Run(_ context.Context, path string, arguments ...string) (string, error) {
	r.calls = append(r.calls, windowsRecordedCommand{path: path, arguments: append([]string(nil), arguments...)})
	key := windowsCommandKey(path, arguments...)
	if err := r.errors[key]; err != nil {
		return "", err
	}
	if output, ok := r.outputs[key]; ok {
		return output, nil
	}
	return r.defaultOutput, nil
}

func TestWindowsOperatorDryRunBuildsStaticPlanWithoutElevation(t *testing.T) {
	runner := &windowsRecordingRunner{outputs: map[string]string{
		windowsCommandKey(powershellPath, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", networkStateScript): windowsDHCPStateFixture,
	}}
	target := config.IPv4Config{
		Mode:    config.IPv4Static,
		Address: "192.168.10.88",
		Netmask: "255.255.255.0",
		Gateway: "192.168.10.1",
		DNS:     []string{"1.1.1.1", "8.8.8.8"},
	}
	current := network.State{Status: network.StateStatusConnected, SSID: "LM-1", Service: "Wi-Fi", Interface: "12"}

	result, err := newOperator(runner, true).Apply(context.Background(), current, target)
	if err != nil {
		t.Fatalf("生成 Windows dry-run 计划失败: %v", err)
	}
	if !result.Success || !result.DryRun || result.Plan == nil || len(result.Plan.Commands) != 5 {
		t.Fatalf("Windows dry-run 结果错误: %#v", result)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("Windows dry-run 执行了写入或提权命令: %#v", runner.calls)
	}
	joined, renderErr := renderPowerShellStatements(result.Plan.Commands)
	if renderErr != nil || !strings.Contains(joined, "New-NetIPAddress") || !strings.Contains(joined, "Set-DnsClientServerAddress") {
		t.Fatalf("Windows 静态配置计划不完整: %q, %v", joined, renderErr)
	}
}

func TestWindowsReaderSeparatesDHCPFromManualDNS(t *testing.T) {
	runner := &windowsRecordingRunner{defaultOutput: `{"IsWiFi":true,"SSID":"Office","Service":"Wi-Fi","Interface":"7","Address":"10.0.0.20","PrefixLength":24,"Gateway":"10.0.0.1","DNS":["1.1.1.1"],"DHCP":"Enabled","DNSManual":true}`}
	state, err := readState(context.Background(), runner)
	if err != nil {
		t.Fatalf("解析 Windows 状态失败: %v", err)
	}
	if state.Mode != network.AddressModeDHCP || state.DNSMode != network.DNSModeManual {
		t.Fatalf("Windows DHCP 与手动 DNS 模式识别错误: %#v", state)
	}
}

func TestWindowsElevatedResultReportsSuccessfulRollback(t *testing.T) {
	runner := &windowsRecordingRunner{defaultOutput: elevatedRollbackSucceeded}
	operator := newOperator(runner, false)
	command := powershellCommand("Set-NetIPInterface -InterfaceIndex 7 -AddressFamily IPv4 -Dhcp Enabled -ErrorAction Stop")
	attempted, succeeded, err := operator.runElevated(context.Background(), []network.CommandPlan{command}, []network.CommandPlan{command})
	if err == nil || !attempted || !succeeded {
		t.Fatalf("Windows 提权回滚结果错误: attempted=%t succeeded=%t err=%v", attempted, succeeded, err)
	}
	if len(runner.calls) != 1 || !strings.Contains(strings.Join(runner.calls[0].arguments, " "), "Start-Process") {
		t.Fatalf("未生成单次 UAC 启动命令: %#v", runner.calls)
	}
}

func TestWindowsOperatorRejectsChangedAdapter(t *testing.T) {
	runner := &windowsRecordingRunner{defaultOutput: windowsDHCPStateFixture}
	current := network.State{Status: network.StateStatusConnected, SSID: "LM-1", Service: "Other Adapter", Interface: "12"}
	_, err := newOperator(runner, false).RestoreDHCP(context.Background(), current)
	if err == nil {
		t.Fatal("预期拒绝已变化的 Windows 网卡映射")
	}
	if len(runner.calls) != 1 {
		t.Fatalf("网卡映射变化后仍执行了写命令: %#v", runner.calls)
	}
}

func windowsCommandKey(path string, arguments ...string) string {
	return path + "\x00" + strings.Join(arguments, "\x00")
}

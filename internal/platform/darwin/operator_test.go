//go:build darwin

package darwin

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/putyy/net-switch/internal/config"
	"github.com/putyy/net-switch/internal/network"
)

const manualInfoFixture = `Manual Configuration
IP address: 192.168.10.88
Subnet mask: 255.255.255.0
Router: 192.168.10.1
IPv6: Automatic
`

type recordedCommand struct {
	path      string
	arguments []string
}

type recordingRunner struct {
	outputs map[string]string
	errors  map[string]error
	calls   []recordedCommand
}

func (r *recordingRunner) Run(_ context.Context, path string, arguments ...string) (string, error) {
	r.calls = append(r.calls, recordedCommand{path: path, arguments: append([]string(nil), arguments...)})
	key := commandKey(path, arguments...)
	if err := r.errors[key]; err != nil {
		return "", err
	}
	return r.outputs[key], nil
}

func TestOperatorDryRunReturnsStructuredPlanWithoutWrites(t *testing.T) {
	runner := &recordingRunner{outputs: map[string]string{
		commandKey(networkSetupPath, "-listnetworkserviceorder"): serviceOrderFixture,
	}}
	current := staticOperationState()
	target := config.IPv4Config{
		Mode:    config.IPv4Static,
		Address: "192.168.10.88",
		Netmask: "255.255.255.0",
		Gateway: "192.168.10.1",
		DNS:     []string{"1.1.1.1", "8.8.8.8"},
	}

	result, err := newOperator(runner, true).Apply(context.Background(), current, target)
	if err != nil {
		t.Fatalf("生成 dry-run 计划失败: %v", err)
	}
	if !result.Success || !result.DryRun || result.Verified || result.Plan == nil {
		t.Fatalf("dry-run 结果错误: %#v", result)
	}
	wanted := []network.CommandPlan{
		networkSetupCommand("-setmanual", "Wi-Fi", "192.168.10.88", "255.255.255.0", "192.168.10.1"),
		networkSetupCommand("-setdnsservers", "Wi-Fi", "1.1.1.1", "8.8.8.8"),
	}
	if !reflect.DeepEqual(result.Plan.Commands, wanted) {
		t.Fatalf("操作计划 = %#v，期望 %#v", result.Plan.Commands, wanted)
	}
	if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0].arguments, []string{"-listnetworkserviceorder"}) {
		t.Fatalf("dry-run 执行了写命令: %#v", runner.calls)
	}
}

func TestOperatorSkipsConfigurationThatAlreadyMatches(t *testing.T) {
	runner := &recordingRunner{outputs: map[string]string{
		commandKey(networkSetupPath, "-listnetworkserviceorder"): serviceOrderFixture,
	}}
	current := staticOperationState()
	target := config.IPv4Config{
		Mode:    config.IPv4Static,
		Address: current.IPv4Address,
		Netmask: current.Netmask,
		Gateway: current.Gateway,
		DNS:     append([]string(nil), current.DNS...),
	}

	result, err := newOperator(runner, false).Apply(context.Background(), current, target)
	if err != nil {
		t.Fatalf("比较已有配置失败: %v", err)
	}
	if !result.Success || !result.Verified || result.DryRun || result.Comparison == nil || !result.Comparison.Matches {
		t.Fatalf("重复执行判断错误: %#v", result)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("相同配置仍执行了系统写命令: %#v", runner.calls)
	}
}

func TestOperatorAppliesAndVerifiesStaticConfiguration(t *testing.T) {
	target := config.IPv4Config{
		Mode:    config.IPv4Static,
		Address: "192.168.10.88",
		Netmask: "255.255.255.0",
		Gateway: "192.168.10.1",
		DNS:     []string{"1.1.1.1", "8.8.8.8"},
	}
	runner := &recordingRunner{outputs: map[string]string{
		commandKey(networkSetupPath, "-listnetworkserviceorder"): serviceOrderFixture,
		commandKey(networkSetupPath, "-getinfo", "Wi-Fi"):        manualInfoFixture,
		commandKey(networkSetupPath, "-getdnsservers", "Wi-Fi"):  "1.1.1.1\n8.8.8.8\n",
	}}
	current := network.State{
		Status:      network.StateStatusConnected,
		Service:     "Wi-Fi",
		Interface:   "en0",
		Mode:        network.AddressModeDHCP,
		DNSMode:     network.DNSModeAutomatic,
		IPv4Address: "192.168.10.66",
		Netmask:     "255.255.255.0",
		Gateway:     "192.168.10.1",
		DNS:         []string{"192.168.10.1"},
	}

	result, err := newOperator(runner, false).Apply(context.Background(), current, target)
	if err != nil {
		t.Fatalf("应用静态配置失败: %v", err)
	}
	if !result.Success || !result.Verified || result.Comparison == nil || !result.Comparison.Matches {
		t.Fatalf("应用结果错误: %#v", result)
	}
	wantedCalls := [][]string{
		{"-listnetworkserviceorder"},
		{"-setmanual", "Wi-Fi", "192.168.10.88", "255.255.255.0", "192.168.10.1"},
		{"-setdnsservers", "Wi-Fi", "1.1.1.1", "8.8.8.8"},
		{"-getinfo", "Wi-Fi"},
		{"-getdnsservers", "Wi-Fi"},
	}
	if got := recordedArguments(runner.calls); !reflect.DeepEqual(got, wantedCalls) {
		t.Fatalf("系统命令参数 = %#v，期望 %#v", got, wantedCalls)
	}
}

func TestOperatorRollsBackAfterPartialFailure(t *testing.T) {
	target := config.IPv4Config{
		Mode:    config.IPv4Static,
		Address: "192.168.10.88",
		Netmask: "255.255.255.0",
		Gateway: "192.168.10.1",
		DNS:     []string{"1.1.1.1"},
	}
	runner := &recordingRunner{
		outputs: map[string]string{
			commandKey(networkSetupPath, "-listnetworkserviceorder"): serviceOrderFixture,
		},
		errors: map[string]error{
			commandKey(networkSetupPath, "-setdnsservers", "Wi-Fi", "1.1.1.1"): errors.New("authorization denied"),
		},
	}
	current := network.State{
		Status:      network.StateStatusConnected,
		Service:     "Wi-Fi",
		Interface:   "en0",
		Mode:        network.AddressModeDHCP,
		DNSMode:     network.DNSModeAutomatic,
		IPv4Address: "192.168.10.66",
		Netmask:     "255.255.255.0",
		Gateway:     "192.168.10.1",
		DNS:         []string{"192.168.10.1"},
	}

	result, err := newOperator(runner, false).Apply(context.Background(), current, target)
	if err == nil {
		t.Fatal("预期 DNS 写入失败")
	}
	if result.Success || !result.RollbackAttempted || !result.RollbackSucceeded {
		t.Fatalf("部分失败后的回滚结果错误: %#v", result)
	}
	wantedSuffix := [][]string{
		{"-setdhcp", "Wi-Fi"},
		{"-setdnsservers", "Wi-Fi", "Empty"},
	}
	got := recordedArguments(runner.calls)
	if len(got) < len(wantedSuffix) || !reflect.DeepEqual(got[len(got)-len(wantedSuffix):], wantedSuffix) {
		t.Fatalf("未执行预期回滚命令: %#v", got)
	}
}

func TestOperatorRejectsChangedServiceMapping(t *testing.T) {
	runner := &recordingRunner{outputs: map[string]string{
		commandKey(networkSetupPath, "-listnetworkserviceorder"): stringsWithoutWiFiService(),
	}}
	_, err := newOperator(runner, false).RestoreDHCP(context.Background(), staticOperationState())
	if err == nil {
		t.Fatal("预期拒绝已变化的 service/interface 对应关系")
	}
	if len(runner.calls) != 1 {
		t.Fatalf("映射无效后仍执行了写命令: %#v", runner.calls)
	}
}

func staticOperationState() network.State {
	return network.State{
		Status:      network.StateStatusConnected,
		Service:     "Wi-Fi",
		Interface:   "en0",
		Mode:        network.AddressModeStatic,
		DNSMode:     network.DNSModeManual,
		IPv4Address: "192.168.10.66",
		Netmask:     "255.255.255.0",
		Gateway:     "192.168.10.1",
		DNS:         []string{"192.168.10.1"},
	}
}

func recordedArguments(calls []recordedCommand) [][]string {
	result := make([][]string, len(calls))
	for index, call := range calls {
		result[index] = call.arguments
	}
	return result
}

func stringsWithoutWiFiService() string {
	return `(1) USB 10/100/1000 LAN
(Hardware Port: USB 10/100/1000 LAN, Device: en5)
`
}

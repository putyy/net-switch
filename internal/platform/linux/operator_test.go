//go:build linux

package linux

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/putyy/net-switch/internal/config"
	"github.com/putyy/net-switch/internal/network"
)

const linuxConnectionUUID = "12345678-1234-1234-1234-123456789abc"

type linuxQueuedResult struct {
	output string
	err    error
}

type linuxRecordedCommand struct {
	path      string
	arguments []string
}

type linuxQueueRunner struct {
	results []linuxQueuedResult
	calls   []linuxRecordedCommand
}

func (r *linuxQueueRunner) Run(_ context.Context, path string, arguments ...string) (string, error) {
	r.calls = append(r.calls, linuxRecordedCommand{path: path, arguments: append([]string(nil), arguments...)})
	if len(r.results) == 0 {
		return "", errors.New("unexpected command")
	}
	result := r.results[0]
	r.results = r.results[1:]
	return result.output, result.err
}

func TestLinuxOperatorDryRunUsesActiveConnectionUUID(t *testing.T) {
	runner := &linuxQueueRunner{results: linuxReadResults()}
	runner.results = append(runner.results,
		linuxQueuedResult{output: "GENERAL.CONNECTION:Office\nGENERAL.CON-UUID:" + linuxConnectionUUID + "\n"},
		linuxQueuedResult{output: "ipv4.method:auto\nipv4.addresses:\nipv4.gateway:\nipv4.dns:\nipv4.ignore-auto-dns:no\n"},
	)
	target := config.IPv4Config{
		Mode:    config.IPv4Static,
		Address: "192.168.10.88",
		Netmask: "255.255.255.0",
		Gateway: "192.168.10.1",
		DNS:     []string{"1.1.1.1"},
	}
	current := network.State{Status: network.StateStatusConnected, SSID: "Office", Service: "Office", Interface: "wlp2s0"}

	result, err := newOperator(runner, true).Apply(context.Background(), current, target)
	if err != nil {
		t.Fatalf("生成 Linux dry-run 计划失败: %v", err)
	}
	if !result.Success || !result.DryRun || result.Plan == nil || len(result.Plan.Commands) != 2 {
		t.Fatalf("Linux dry-run 结果错误: %#v", result)
	}
	wantedModifyPrefix := []string{"connection", "modify", "uuid", linuxConnectionUUID}
	if got := result.Plan.Commands[0].Arguments[:len(wantedModifyPrefix)]; !reflect.DeepEqual(got, wantedModifyPrefix) {
		t.Fatalf("Linux 配置未使用连接 UUID: %#v", result.Plan.Commands[0])
	}
	if len(runner.results) != 0 {
		t.Fatalf("Linux dry-run 未完成预期读取: %#v", runner.results)
	}
	for _, call := range runner.calls {
		if len(call.arguments) >= 2 && call.arguments[0] == "connection" && call.arguments[1] == "modify" {
			t.Fatalf("Linux dry-run 执行了写命令: %#v", runner.calls)
		}
	}
}

func TestLinuxProfileSeparatesDHCPFromManualDNS(t *testing.T) {
	state := network.State{}
	applyProfile(&state, "ipv4.method:auto\nipv4.dns:1.1.1.1\nipv4.ignore-auto-dns:no\n")
	if state.Mode != network.AddressModeDHCP || state.DNSMode != network.DNSModeManual {
		t.Fatalf("Linux DHCP 与手动 DNS 模式识别错误: %#v", state)
	}
}

func TestLinuxSnapshotRollbackPreservesProfile(t *testing.T) {
	snapshot := connectionSnapshot{
		Method:        "manual",
		Addresses:     "192.168.10.50/24",
		Gateway:       "192.168.10.1",
		DNS:           "223.5.5.5,8.8.8.8",
		IgnoreAutoDNS: "yes",
	}
	commands, err := linuxCommandsForSnapshot(linuxConnectionUUID, "wlp2s0", snapshot)
	if err != nil {
		t.Fatalf("生成 Linux 回滚计划失败: %v", err)
	}
	if len(commands) != 2 || commands[0].Executable != nmcliPath || commands[1].Executable != nmcliPath {
		t.Fatalf("Linux 回滚计划不完整: %#v", commands)
	}
}

func TestLinuxOperatorRollsBackWhenReactivationFails(t *testing.T) {
	runner := &linuxQueueRunner{results: linuxReadResults()}
	runner.results = append(runner.results,
		linuxQueuedResult{output: "GENERAL.CONNECTION:Office\nGENERAL.CON-UUID:" + linuxConnectionUUID + "\n"},
		linuxQueuedResult{output: "ipv4.method:auto\nipv4.addresses:\nipv4.gateway:\nipv4.dns:\nipv4.ignore-auto-dns:no\n"},
		linuxQueuedResult{},
		linuxQueuedResult{err: errors.New("activation failed")},
		linuxQueuedResult{},
		linuxQueuedResult{},
	)
	target := config.IPv4Config{
		Mode:    config.IPv4Static,
		Address: "192.168.10.88",
		Netmask: "255.255.255.0",
		Gateway: "192.168.10.1",
		DNS:     []string{"1.1.1.1"},
	}
	current := network.State{Status: network.StateStatusConnected, SSID: "Office", Service: "Office", Interface: "wlp2s0"}

	result, err := newOperator(runner, false).Apply(context.Background(), current, target)
	if err == nil || !result.RollbackAttempted || !result.RollbackSucceeded {
		t.Fatalf("Linux 重新激活失败后的回滚结果错误: %#v err=%v", result, err)
	}
	if len(runner.results) != 0 {
		t.Fatalf("Linux 回滚命令未完整执行: %#v", runner.results)
	}
}

func TestLinuxAuthorizationErrorRecognition(t *testing.T) {
	if !isLinuxAuthorizationError(errors.New("Error: Not authorized to control networking")) {
		t.Fatal("未识别 NetworkManager 授权失败")
	}
	if isLinuxAuthorizationError(errors.New("connection activation timed out")) {
		t.Fatal("普通连接错误不应识别为授权失败")
	}
}

func linuxReadResults() []linuxQueuedResult {
	return []linuxQueuedResult{
		{output: "wlp2s0:wifi:connected:Office\n"},
		{output: "GENERAL.CONNECTION:Office\nGENERAL.CON-UUID:" + linuxConnectionUUID + "\n"},
		{output: "*:Office\n"},
		{output: "IP4.ADDRESS[1]:192.168.10.50/24\nIP4.GATEWAY:192.168.10.1\nIP4.DNS[1]:192.168.10.1\n"},
		{output: "ipv4.method:auto\nipv4.dns:\nipv4.ignore-auto-dns:no\n"},
	}
}

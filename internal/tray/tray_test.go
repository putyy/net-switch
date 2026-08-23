package tray

import (
	"strings"
	"testing"

	"github.com/putyy/net-switch/internal/config"
	"github.com/putyy/net-switch/internal/network"
)

func TestNetworkLabel(t *testing.T) {
	tests := []struct {
		name  string
		state network.State
		want  string
	}{
		{name: "ssid", state: network.State{Status: network.StateStatusConnected, SSID: "Office-WiFi"}, want: "Office-WiFi"},
		{name: "disconnected", state: network.State{Status: network.StateStatusDisconnected}, want: "未连接"},
		{name: "unavailable", state: network.State{Status: network.StateStatusUnavailable}, want: "状态不可用"},
		{name: "wired", state: network.State{Status: network.StateStatusConnected, Service: "USB LAN"}, want: "USB LAN"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := networkLabel(labelsFor(config.LanguageChinese), test.state); got != test.want {
				t.Fatalf("菜单网络状态 = %q，期望 %q", got, test.want)
			}
		})
	}
}

func TestCleanMenuValueRemovesControlsAndLimitsLength(t *testing.T) {
	got := cleanMenuValue(" Office\n" + strings.Repeat("网", 50))
	if strings.ContainsRune(got, '\n') || len([]rune(got)) != 40 || !strings.HasSuffix(got, "…") {
		t.Fatalf("菜单文本清理结果不正确: %q", got)
	}
}

func TestAutoSwitchActionLabel(t *testing.T) {
	if got := autoSwitchActionLabel(labelsFor(config.LanguageChinese), true); got != "暂停自动切换" {
		t.Fatalf("开启状态菜单文字 = %q", got)
	}
	if got := autoSwitchActionLabel(labelsFor(config.LanguageChinese), false); got != "恢复自动切换" {
		t.Fatalf("暂停状态菜单文字 = %q", got)
	}
}

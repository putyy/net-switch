package tray

import (
	"github.com/putyy/net-switch/internal/config"
)

type labels struct {
	CurrentNetwork       string
	CurrentIPv4          string
	AutoSwitch           string
	OpenDashboard        string
	OpenDashboardTip     string
	ApplyRule            string
	ApplyRuleTip         string
	RestoreDHCP          string
	RestoreDHCPTip       string
	ToggleAutoSwitchTip  string
	AutoStart            string
	AutoStartTip         string
	Quit                 string
	ConnectedUnknownWiFi string
	Disconnected         string
	Unavailable          string
	Reading              string
	NoIPv4               string
	Enabled              string
	Paused               string
	PauseAutoSwitch      string
	ResumeAutoSwitch     string
}

func labelsFor(language config.Language) labels {
	if language == config.LanguageEnglish {
		return labels{
			CurrentNetwork:       "Current network",
			CurrentIPv4:          "Current IPv4",
			AutoSwitch:           "Auto switch",
			OpenDashboard:        "Open Dashboard",
			OpenDashboardTip:     "Open Net Switch in the default browser",
			ApplyRule:            "Apply Matched Rule",
			ApplyRuleTip:         "Apply the enabled rule for the current Wi-Fi",
			RestoreDHCP:          "Restore DHCP",
			RestoreDHCPTip:       "Restore DHCP and automatic DNS for the current Wi-Fi service",
			ToggleAutoSwitchTip:  "Pause or resume automatic network switching",
			AutoStart:            "Start at Login",
			AutoStartTip:         "Start Net Switch after signing in",
			Quit:                 "Quit",
			ConnectedUnknownWiFi: "Non-Wi-Fi network",
			Disconnected:         "Disconnected",
			Unavailable:          "Unavailable",
			Reading:              "Reading",
			NoIPv4:               "No IPv4",
			Enabled:              "On",
			Paused:               "Paused",
			PauseAutoSwitch:      "Pause Auto Switch",
			ResumeAutoSwitch:     "Resume Auto Switch",
		}
	}
	return labels{
		CurrentNetwork:       "当前网络",
		CurrentIPv4:          "当前 IPv4",
		AutoSwitch:           "自动切换",
		OpenDashboard:        "打开控制面板",
		OpenDashboardTip:     "使用默认浏览器打开 Net Switch",
		ApplyRule:            "立即应用",
		ApplyRuleTip:         "应用当前 Wi-Fi 匹配的启用规则",
		RestoreDHCP:          "恢复 DHCP",
		RestoreDHCPTip:       "将当前 Wi-Fi 网络服务恢复为 DHCP 和自动 DNS",
		ToggleAutoSwitchTip:  "暂停或恢复自动网络切换",
		AutoStart:            "开机启动",
		AutoStartTip:         "登录系统后自动启动 Net Switch",
		Quit:                 "退出",
		ConnectedUnknownWiFi: "非 Wi-Fi 网络",
		Disconnected:         "未连接",
		Unavailable:          "状态不可用",
		Reading:              "读取中",
		NoIPv4:               "无 IPv4",
		Enabled:              "已开启",
		Paused:               "已暂停",
		PauseAutoSwitch:      "暂停自动切换",
		ResumeAutoSwitch:     "恢复自动切换",
	}
}

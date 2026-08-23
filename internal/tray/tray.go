package tray

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"fyne.io/systray"
	"github.com/putyy/net-switch/internal/config"
	"github.com/putyy/net-switch/internal/network"
)

type Status struct {
	Network    network.State
	AutoSwitch bool
	Language   config.Language
}

type Actions struct {
	RequestPermissions func()
	OpenDashboard      func()
	ApplyMatchedRule   func() error
	RestoreDHCP        func() error
	ToggleAutoSwitch   func() error
	AutoStartAvailable bool
	AutoStartEnabled   bool
	AutoStartUpdates   <-chan bool
	SetAutoStart       func(bool) error
	InitialStatus      Status
	StatusUpdates      <-chan Status
}

func Run(ctx context.Context, actions Actions) {
	systray.Run(func() {
		setTrayIcon()
		systray.SetTitle("")
		systray.SetTooltip("Net Switch")

		header := systray.AddMenuItem("Net Switch", "")
		header.Disable()
		currentNetwork := systray.AddMenuItem("", "")
		currentNetwork.Disable()
		currentIPv4 := systray.AddMenuItem("", "")
		currentIPv4.Disable()
		autoSwitchStatus := systray.AddMenuItem("", "")
		autoSwitchStatus.Disable()
		systray.AddSeparator()
		openDashboard := systray.AddMenuItem("", "")
		applyRule := systray.AddMenuItem("", "")
		restoreDHCP := systray.AddMenuItem("", "")
		toggleAutoSwitch := systray.AddMenuItem("", "")
		if actions.ApplyMatchedRule == nil {
			applyRule.Disable()
		}
		if actions.RestoreDHCP == nil {
			restoreDHCP.Disable()
		}
		if actions.ToggleAutoSwitch == nil {
			toggleAutoSwitch.Disable()
		}
		systray.AddSeparator()
		autoStart := systray.AddMenuItemCheckbox("", "", actions.AutoStartEnabled)
		if !actions.AutoStartAvailable {
			autoStart.Disable()
		}
		systray.AddSeparator()
		quit := systray.AddMenuItem("", "")
		applyStatus(currentNetwork, currentIPv4, autoSwitchStatus, actions.InitialStatus)
		applyMenuLanguage(openDashboard, applyRule, restoreDHCP, toggleAutoSwitch, autoStart, quit, actions.InitialStatus)
		if actions.RequestPermissions != nil {
			actions.RequestPermissions()
		}

		go func() {
			statusUpdates := actions.StatusUpdates
			autoStartUpdates := actions.AutoStartUpdates
			networkActionDone := make(chan error, 1)
			networkActionRunning := false
			startNetworkAction := func(action func() error) {
				if networkActionRunning || action == nil {
					return
				}
				networkActionRunning = true
				applyRule.Disable()
				restoreDHCP.Disable()
				go func() {
					networkActionDone <- action()
				}()
			}
			for {
				select {
				case <-ctx.Done():
					systray.Quit()
					return
				case <-openDashboard.ClickedCh:
					if actions.OpenDashboard != nil {
						actions.OpenDashboard()
					}
				case <-applyRule.ClickedCh:
					startNetworkAction(actions.ApplyMatchedRule)
				case <-restoreDHCP.ClickedCh:
					startNetworkAction(actions.RestoreDHCP)
				case <-toggleAutoSwitch.ClickedCh:
					if actions.ToggleAutoSwitch != nil {
						_ = actions.ToggleAutoSwitch()
					}
				case <-networkActionDone:
					networkActionRunning = false
					if actions.ApplyMatchedRule != nil {
						applyRule.Enable()
					}
					if actions.RestoreDHCP != nil {
						restoreDHCP.Enable()
					}
				case <-autoStart.ClickedCh:
					enabled := !autoStart.Checked()
					if actions.SetAutoStart != nil && actions.SetAutoStart(enabled) == nil {
						if enabled {
							autoStart.Check()
						} else {
							autoStart.Uncheck()
						}
					}
				case <-quit.ClickedCh:
					systray.Quit()
					return
				case status, ok := <-statusUpdates:
					if !ok {
						statusUpdates = nil
						continue
					}
					applyStatus(currentNetwork, currentIPv4, autoSwitchStatus, status)
					applyMenuLanguage(openDashboard, applyRule, restoreDHCP, toggleAutoSwitch, autoStart, quit, status)
				case enabled, ok := <-autoStartUpdates:
					if !ok {
						autoStartUpdates = nil
						continue
					}
					if enabled {
						autoStart.Check()
					} else {
						autoStart.Uncheck()
					}
				}
			}
		}()
	}, func() {})
}

func applyMenuLanguage(openDashboard, applyRule, restoreDHCP, toggleAutoSwitch, autoStart, quit *systray.MenuItem, status Status) {
	text := labelsFor(status.Language)
	openDashboard.SetTitle(text.OpenDashboard)
	openDashboard.SetTooltip(text.OpenDashboardTip)
	applyRule.SetTitle(text.ApplyRule)
	applyRule.SetTooltip(text.ApplyRuleTip)
	restoreDHCP.SetTitle(text.RestoreDHCP)
	restoreDHCP.SetTooltip(text.RestoreDHCPTip)
	toggleAutoSwitch.SetTitle(autoSwitchActionLabel(text, status.AutoSwitch))
	toggleAutoSwitch.SetTooltip(text.ToggleAutoSwitchTip)
	autoStart.SetTitle(text.AutoStart)
	autoStart.SetTooltip(text.AutoStartTip)
	quit.SetTitle(text.Quit)
}

func autoSwitchActionLabel(text labels, enabled bool) string {
	if enabled {
		return text.PauseAutoSwitch
	}
	return text.ResumeAutoSwitch
}

func applyStatus(networkItem, ipv4Item, autoSwitchItem *systray.MenuItem, status Status) {
	text := labelsFor(status.Language)
	networkItem.SetTitle(text.CurrentNetwork + ": " + networkLabel(text, status.Network))
	ipv4Item.SetTitle(text.CurrentIPv4 + ": " + valueOr(status.Network.IPv4Address, "—"))
	autoSwitchLabel := text.Paused
	if status.AutoSwitch {
		autoSwitchLabel = text.Enabled
	}
	autoSwitchItem.SetTitle(text.AutoSwitch + ": " + autoSwitchLabel)
	systray.SetTooltip(fmt.Sprintf("Net Switch · %s · %s", networkLabel(text, status.Network), valueOr(status.Network.IPv4Address, text.NoIPv4)))
}

func networkLabel(text labels, state network.State) string {
	if state.SSID != "" {
		return cleanMenuValue(state.SSID)
	}
	switch state.Status {
	case network.StateStatusDisconnected:
		return text.Disconnected
	case network.StateStatusUnavailable:
		return text.Unavailable
	case network.StateStatusConnected:
		return valueOr(state.Service, text.ConnectedUnknownWiFi)
	default:
		return text.Reading
	}
}

func valueOr(value, fallback string) string {
	cleaned := cleanMenuValue(value)
	if cleaned == "" {
		return fallback
	}
	return cleaned
}

func cleanMenuValue(value string) string {
	cleaned := strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return ' '
		}
		return character
	}, strings.TrimSpace(value))
	runes := []rune(cleaned)
	const maxRunes = 40
	if len(runes) > maxRunes {
		cleaned = string(runes[:maxRunes-1]) + "…"
	}
	return cleaned
}

func Quit() {
	systray.Quit()
}

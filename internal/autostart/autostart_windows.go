//go:build windows

package autostart

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

const windowsRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`

type Manager struct {
	executable string
}

func New(_ bool) (*Manager, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("get executable path: %w", err)
	}
	return &Manager{executable: executable}, nil
}

func (m *Manager) Enabled() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, windowsRunKey, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("open start-at-login registry key: %w", err)
	}
	defer key.Close()
	_, _, err = key.GetStringValue("NetSwitch")
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read start-at-login status: %w", err)
	}
	return true, nil
}

func (m *Manager) SetEnabled(enabled bool) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, windowsRunKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open start-at-login registry key: %w", err)
	}
	defer key.Close()
	if enabled {
		value := `"` + m.executable + `" --login-start`
		if err := key.SetStringValue("NetSwitch", value); err != nil {
			return fmt.Errorf("enable start at login: %w", err)
		}
		return nil
	}
	if err := key.DeleteValue("NetSwitch"); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("disable start at login: %w", err)
	}
	return nil
}

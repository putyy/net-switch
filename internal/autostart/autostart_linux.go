//go:build linux

package autostart

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Manager struct {
	executable string
	entryPath  string
}

func New(_ bool) (*Manager, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("get executable path: %w", err)
	}
	configDirectory, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("get user configuration directory: %w", err)
	}
	return &Manager{
		executable: executable,
		entryPath:  filepath.Join(configDirectory, "autostart", "net-switch.desktop"),
	}, nil
}

func (m *Manager) Enabled() (bool, error) {
	_, err := os.Stat(m.entryPath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("read start-at-login status: %w", err)
}

func (m *Manager) SetEnabled(enabled bool) error {
	if !enabled {
		if err := os.Remove(m.entryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("disable start at login: %w", err)
		}
		return nil
	}
	directory := filepath.Dir(m.entryPath)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create autostart directory: %w", err)
	}
	contents := "[Desktop Entry]\nType=Application\nName=Net Switch\nExec=" + desktopQuote(m.executable) + " --login-start\nTerminal=false\nX-GNOME-Autostart-enabled=true\n"
	if err := os.WriteFile(m.entryPath, []byte(contents), 0o600); err != nil {
		return fmt.Errorf("save start-at-login configuration: %w", err)
	}
	return nil
}

func desktopQuote(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "`", "\\`", "$", "\\$")
	return "\"" + replacer.Replace(value) + "\""
}

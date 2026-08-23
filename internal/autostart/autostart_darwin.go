//go:build darwin

package autostart

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
)

const (
	label         = "com.putyy.net-switch"
	launchctl     = "/bin/launchctl"
	loginFlag     = "--login-start"
	plistPerm     = 0o644
	directoryPerm = 0o755
)

type Manager struct {
	executable      string
	plistPath       string
	domain          string
	serviceTarget   string
	launchedAtLogin bool
}

func New(launchedAtLogin bool) (*Manager, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("get executable path: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return nil, fmt.Errorf("resolve executable path: %w", err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get user home directory: %w", err)
	}
	domain := "gui/" + strconv.Itoa(os.Getuid())
	return &Manager{
		executable:      executable,
		plistPath:       filepath.Join(homeDir, "Library", "LaunchAgents", label+".plist"),
		domain:          domain,
		serviceTarget:   domain + "/" + label,
		launchedAtLogin: launchedAtLogin,
	}, nil
}

func (m *Manager) Enabled() (bool, error) {
	_, err := os.Stat(m.plistPath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("read start-at-login status: %w", err)
}

func (m *Manager) SetEnabled(enabled bool) error {
	if enabled {
		return m.enable()
	}
	return m.disable()
}

func (m *Manager) enable() error {
	directory := filepath.Dir(m.plistPath)
	if err := os.MkdirAll(directory, directoryPerm); err != nil {
		return fmt.Errorf("create LaunchAgents directory: %w", err)
	}

	contents, err := renderPlist(m.executable)
	if err != nil {
		return err
	}
	if err := writeAtomic(m.plistPath, contents); err != nil {
		return fmt.Errorf("save start-at-login configuration: %w", err)
	}

	if m.serviceLoaded() {
		return nil
	}
	if output, err := exec.Command(launchctl, "bootstrap", m.domain, m.plistPath).CombinedOutput(); err != nil {
		_ = os.Remove(m.plistPath)
		return fmt.Errorf("load start-at-login configuration: %w: %s", err, bytes.TrimSpace(output))
	}
	return nil
}

func (m *Manager) disable() error {
	if err := os.Remove(m.plistPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete start-at-login configuration: %w", err)
	}

	// launchctl bootout 会终止由该 LaunchAgent 启动的当前进程。
	// 此时删除 plist 已足以保证下次登录不再启动，当前客户端继续运行。
	if m.launchedAtLogin || !m.serviceLoaded() {
		return nil
	}
	if output, err := exec.Command(launchctl, "bootout", m.serviceTarget).CombinedOutput(); err != nil {
		return fmt.Errorf("unload start-at-login configuration: %w: %s", err, bytes.TrimSpace(output))
	}
	return nil
}

func (m *Manager) serviceLoaded() bool {
	command := exec.Command(launchctl, "print", m.serviceTarget)
	command.Stdout = nil
	command.Stderr = nil
	return command.Run() == nil
}

func renderPlist(executable string) ([]byte, error) {
	var escaped bytes.Buffer
	if err := escapeXML(&escaped, executable); err != nil {
		return nil, fmt.Errorf("encode executable path: %w", err)
	}

	contents := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>` + label + `</string>
    <key>ProgramArguments</key>
    <array>
        <string>` + escaped.String() + `</string>
        <string>` + loginFlag + `</string>
    </array>
    <key>ProcessType</key>
    <string>Interactive</string>
    <key>RunAtLoad</key>
    <true/>
</dict>
</plist>
`
	return []byte(contents), nil
}

func escapeXML(target *bytes.Buffer, value string) error {
	for _, character := range value {
		switch character {
		case '&':
			target.WriteString("&amp;")
		case '<':
			target.WriteString("&lt;")
		case '>':
			target.WriteString("&gt;")
		case '"':
			target.WriteString("&quot;")
		case '\'':
			target.WriteString("&apos;")
		default:
			if character < 0x20 && character != '\t' && character != '\n' && character != '\r' {
				return fmt.Errorf("contains a control character that is not allowed in XML")
			}
			target.WriteRune(character)
		}
	}
	return nil
}

func writeAtomic(path string, contents []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".net-switch-*.plist")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(plistPerm); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

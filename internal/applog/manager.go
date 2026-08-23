package applog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/putyy/net-switch/internal/config"
)

const (
	DirectoryName       = "logs"
	FileName            = "net-switch.log"
	defaultMaxFileSize  = int64(1024 * 1024)
	defaultMaxBackups   = 3
	directoryPermission = 0o700
	filePermission      = 0o600
)

var errClosed = errors.New("log manager is closed")

type Manager struct {
	mu          sync.Mutex
	directory   string
	path        string
	file        *os.File
	size        int64
	maxFileSize int64
	maxBackups  int
}

func New() (*Manager, error) {
	configHome, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("get user configuration directory: %w", err)
	}
	return NewAt(filepath.Join(configHome, config.DirectoryName, DirectoryName))
}

func NewAt(directory string) (*Manager, error) {
	return newManagerAt(directory, defaultMaxFileSize, defaultMaxBackups)
}

func newManagerAt(directory string, maxFileSize int64, maxBackups int) (*Manager, error) {
	if directory == "" {
		return nil, errors.New("log directory is required")
	}
	if maxFileSize <= 0 {
		return nil, errors.New("maximum log file size must be positive")
	}
	if maxBackups < 0 {
		return nil, errors.New("log backup count cannot be negative")
	}
	if err := os.MkdirAll(directory, directoryPermission); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	if err := os.Chmod(directory, directoryPermission); err != nil {
		return nil, fmt.Errorf("set log directory permissions: %w", err)
	}

	manager := &Manager{
		directory:   directory,
		path:        filepath.Join(directory, FileName),
		maxFileSize: maxFileSize,
		maxBackups:  maxBackups,
	}
	if err := manager.openCurrent(); err != nil {
		return nil, err
	}
	return manager, nil
}

func (m *Manager) Write(contents []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file == nil {
		return 0, errClosed
	}

	written := 0
	for len(contents) > 0 {
		if m.size >= m.maxFileSize {
			if err := m.rotateLocked(); err != nil {
				return written, err
			}
		}
		available := m.maxFileSize - m.size
		chunkSize := int64(len(contents))
		if chunkSize > available {
			chunkSize = available
		}
		count, err := m.file.Write(contents[:int(chunkSize)])
		written += count
		m.size += int64(count)
		contents = contents[count:]
		if err != nil {
			return written, fmt.Errorf("write log file: %w", err)
		}
		if count == 0 {
			return written, errors.New("log file write produced no data")
		}
	}
	return written, nil
}

func (m *Manager) Recent(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		return []string{}, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file == nil {
		return nil, errClosed
	}

	var contents bytes.Buffer
	for index := m.maxBackups; index >= 1; index-- {
		if err := appendLogFile(&contents, m.backupPath(index)); err != nil {
			return nil, err
		}
	}
	if err := appendLogFile(&contents, m.path); err != nil {
		return nil, err
	}
	trimmed := strings.TrimRight(contents.String(), "\r\n")
	if trimmed == "" {
		return []string{}, nil
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	for index := range lines {
		lines[index] = strings.TrimSuffix(lines[index], "\r")
	}
	return lines, nil
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.file == nil {
		return nil
	}
	err := m.file.Close()
	m.file = nil
	if err != nil {
		return fmt.Errorf("close log file: %w", err)
	}
	return nil
}

func (m *Manager) rotateLocked() error {
	if err := m.file.Close(); err != nil {
		m.file = nil
		return fmt.Errorf("close log file before rotation: %w", err)
	}
	m.file = nil

	if m.maxBackups > 0 {
		if err := os.Remove(m.backupPath(m.maxBackups)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("delete oldest log backup: %w", err)
		}
		for index := m.maxBackups - 1; index >= 1; index-- {
			if err := renameIfExists(m.backupPath(index), m.backupPath(index+1)); err != nil {
				return err
			}
		}
		if err := renameIfExists(m.path, m.backupPath(1)); err != nil {
			return err
		}
	} else if err := os.Remove(m.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear full log file: %w", err)
	}
	return m.openCurrent()
}

func (m *Manager) openCurrent() error {
	file, err := os.OpenFile(m.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, filePermission)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	if err := file.Chmod(filePermission); err != nil {
		_ = file.Close()
		return fmt.Errorf("set log file permissions: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("stat log file: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return errors.New("log path is not a regular file")
	}
	m.file = file
	m.size = info.Size()
	return nil
}

func (m *Manager) backupPath(index int) string {
	return fmt.Sprintf("%s.%d", m.path, index)
}

func appendLogFile(destination *bytes.Buffer, path string) error {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read recent logs: %w", err)
	}
	_, _ = destination.Write(contents)
	return nil
}

func renameIfExists(source, destination string) error {
	if err := os.Rename(source, destination); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("rotate log file: %w", err)
	}
	return nil
}

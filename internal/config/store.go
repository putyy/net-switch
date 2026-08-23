package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/pelletier/go-toml/v2"
)

const (
	DirectoryName = "Net Switch"
	FileName      = "config.toml"
	maxFileSize   = 1024 * 1024
	directoryPerm = 0o700
	filePerm      = 0o600
)

type Store struct {
	path string
}

func NewStore() (*Store, error) {
	configHome, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("get user configuration directory: %w", err)
	}
	return NewStoreAt(filepath.Join(configHome, DirectoryName)), nil
}

func NewStoreAt(directory string) *Store {
	return &Store{path: filepath.Join(directory, FileName)}
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) LoadOrCreate() (Config, error) {
	configuration, err := s.Load()
	if err == nil {
		return configuration, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Config{}, err
	}

	configuration = Default()
	if err := s.Save(configuration); err != nil {
		return Config{}, err
	}
	return configuration, nil
}

func (s *Store) Load() (Config, error) {
	file, err := os.Open(s.path)
	if err != nil {
		return Config{}, fmt.Errorf("open configuration file: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return Config{}, fmt.Errorf("stat configuration file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Config{}, fmt.Errorf("configuration path is not a regular file")
	}
	if info.Size() > maxFileSize {
		return Config{}, fmt.Errorf("configuration file exceeds the %d-byte limit", maxFileSize)
	}

	contents, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		return Config{}, fmt.Errorf("read configuration file: %w", err)
	}
	if len(contents) > maxFileSize {
		return Config{}, fmt.Errorf("configuration file exceeds the %d-byte limit", maxFileSize)
	}

	var configuration Config
	decoder := toml.NewDecoder(bytes.NewReader(contents)).DisallowUnknownFields()
	if err := decoder.Decode(&configuration); err != nil {
		return Config{}, fmt.Errorf("parse configuration file: %w", err)
	}
	configuration.ApplyDefaults()
	if err := configuration.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate configuration file: %w", err)
	}
	return configuration, nil
}

func (s *Store) Save(configuration Config) error {
	if err := configuration.Validate(); err != nil {
		return fmt.Errorf("validate configuration: %w", err)
	}
	contents, err := toml.Marshal(configuration)
	if err != nil {
		return fmt.Errorf("encode configuration: %w", err)
	}

	directory := filepath.Dir(s.path)
	if err := os.MkdirAll(directory, directoryPerm); err != nil {
		return fmt.Errorf("create configuration directory: %w", err)
	}
	if err := os.Chmod(directory, directoryPerm); err != nil {
		return fmt.Errorf("set configuration directory permissions: %w", err)
	}
	if err := writeAtomic(s.path, contents); err != nil {
		return fmt.Errorf("save configuration file: %w", err)
	}
	return nil
}

func writeAtomic(path string, contents []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".config-*.toml")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(filePerm); err != nil {
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
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return nil
	}

	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}

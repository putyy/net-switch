package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStoreCreatesAndRoundTripsConfiguration(t *testing.T) {
	store := NewStoreAt(t.TempDir())
	created, err := store.LoadOrCreate()
	if err != nil {
		t.Fatalf("创建默认配置失败: %v", err)
	}
	if !reflect.DeepEqual(created, Default()) {
		t.Fatalf("创建的配置与默认值不同: %#v", created)
	}

	configuration := validStaticConfiguration()
	if err := store.Save(configuration); err != nil {
		t.Fatalf("保存配置失败: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}
	if !reflect.DeepEqual(loaded, configuration) {
		t.Fatalf("配置往返后不同:\n读取: %#v\n写入: %#v", loaded, configuration)
	}

	info, err := os.Stat(store.Path())
	if err != nil {
		t.Fatalf("读取配置文件权限失败: %v", err)
	}
	if permission := info.Mode().Perm(); permission != filePerm {
		t.Fatalf("配置文件权限为 %o，预期 %o", permission, filePerm)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(store.Path()), ".config-*.toml"))
	if err != nil {
		t.Fatalf("检查临时文件失败: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("存在未清理的临时文件: %v", matches)
	}
}

func TestStoreRejectsUnknownFieldsWithoutOverwriting(t *testing.T) {
	store := NewStoreAt(t.TempDir())
	contents := `[general]
auto_switch = true
unmatched_action = "keep"
unexpected = true
`
	if err := os.MkdirAll(filepath.Dir(store.Path()), directoryPerm); err != nil {
		t.Fatalf("创建测试目录失败: %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte(contents), filePerm); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}

	_, err := store.Load()
	if err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("预期未知字段错误，得到: %v", err)
	}
	stored, readErr := os.ReadFile(store.Path())
	if readErr != nil {
		t.Fatalf("重新读取测试配置失败: %v", readErr)
	}
	if string(stored) != contents {
		t.Fatal("加载失败时不应覆盖原配置")
	}
}

func TestStoreDoesNotCreateInvalidConfiguration(t *testing.T) {
	store := NewStoreAt(t.TempDir())
	configuration := Default()
	configuration.General.UnmatchedAction = "invalid"

	err := store.Save(configuration)
	if err == nil {
		t.Fatal("预期保存失败")
	}
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("预期 ValidationError，得到 %T: %v", err, err)
	}
	if _, statErr := os.Stat(store.Path()); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("无效配置不应创建文件: %v", statErr)
	}
}

func TestStoreAppliesDefaultLanguageToExistingConfiguration(t *testing.T) {
	store := NewStoreAt(t.TempDir())
	contents := `[general]
auto_switch = true
unmatched_action = "keep"
`
	if err := os.MkdirAll(filepath.Dir(store.Path()), directoryPerm); err != nil {
		t.Fatalf("创建测试目录失败: %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte(contents), filePerm); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}

	configuration, err := store.Load()
	if err != nil {
		t.Fatalf("读取旧配置失败: %v", err)
	}
	if configuration.General.Language != LanguageChinese {
		t.Fatalf("旧配置的默认语言 = %q，期望 %q", configuration.General.Language, LanguageChinese)
	}
}

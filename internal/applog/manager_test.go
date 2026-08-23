package applog

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestManagerReadsRecentLinesAcrossRotatedFiles(t *testing.T) {
	manager, err := newManagerAt(t.TempDir(), 12, 2)
	if err != nil {
		t.Fatalf("创建日志管理器失败: %v", err)
	}
	defer manager.Close()

	if _, err := manager.Write([]byte("one\ntwo\nthree\nfour\n")); err != nil {
		t.Fatalf("写入日志失败: %v", err)
	}
	lines, err := manager.Recent(context.Background(), 3)
	if err != nil {
		t.Fatalf("读取最近日志失败: %v", err)
	}
	if wanted := []string{"two", "three", "four"}; !reflect.DeepEqual(lines, wanted) {
		t.Fatalf("最近日志 = %#v，期望 %#v", lines, wanted)
	}
}

func TestManagerKeepsConfiguredBackupCount(t *testing.T) {
	directory := t.TempDir()
	manager, err := newManagerAt(directory, 5, 2)
	if err != nil {
		t.Fatalf("创建日志管理器失败: %v", err)
	}
	if _, err := manager.Write([]byte("12345678901234567890")); err != nil {
		t.Fatalf("写入日志失败: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("关闭日志管理器失败: %v", err)
	}

	if _, err := os.Stat(filepath.Join(directory, FileName+".2")); err != nil {
		t.Fatalf("缺少第二份历史日志: %v", err)
	}
	if _, err := os.Stat(filepath.Join(directory, FileName+".3")); !os.IsNotExist(err) {
		t.Fatalf("保留了超出限制的历史日志: %v", err)
	}
}

func TestManagerCreatesPrivateDirectoryAndFile(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "logs")
	manager, err := NewAt(directory)
	if err != nil {
		t.Fatalf("创建日志管理器失败: %v", err)
	}
	defer manager.Close()

	directoryInfo, err := os.Stat(directory)
	if err != nil {
		t.Fatalf("读取日志目录权限失败: %v", err)
	}
	if directoryInfo.Mode().Perm() != directoryPermission {
		t.Fatalf("日志目录权限 = %v，期望 %v", directoryInfo.Mode().Perm(), directoryPermission)
	}
	fileInfo, err := os.Stat(filepath.Join(directory, FileName))
	if err != nil {
		t.Fatalf("读取日志文件权限失败: %v", err)
	}
	if fileInfo.Mode().Perm() != filePermission {
		t.Fatalf("日志文件权限 = %v，期望 %v", fileInfo.Mode().Perm(), filePermission)
	}
}

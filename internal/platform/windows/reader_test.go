//go:build windows

package windows

import "testing"

func TestPrefixMask(t *testing.T) {
	if mask := prefixMask(24); mask != "255.255.255.0" {
		t.Fatalf("前缀掩码转换错误: %q", mask)
	}
	if mask := prefixMask(33); mask != "" {
		t.Fatalf("无效前缀不应返回掩码: %q", mask)
	}
}

func TestNetmaskPrefix(t *testing.T) {
	prefix, err := netmaskPrefix("255.255.255.0")
	if err != nil || prefix != 24 {
		t.Fatalf("掩码前缀转换错误: prefix=%d err=%v", prefix, err)
	}
	if _, err := netmaskPrefix("255.0.255.0"); err == nil {
		t.Fatal("非连续掩码不应通过校验")
	}
}

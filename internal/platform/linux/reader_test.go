//go:build linux

package linux

import "testing"

func TestParseActiveWiFiAndSSID(t *testing.T) {
	device, connection, ok := parseActiveWiFi("enp3s0:ethernet:connected:Wired connection 1\nwlp2s0:wifi:connected:Office\\:5G\n")
	if !ok || device != "wlp2s0" || connection != "Office:5G" {
		t.Fatalf("活动 Wi-Fi 解析错误: device=%q connection=%q ok=%t", device, connection, ok)
	}
	if ssid := parseActiveSSID(" :Guest\n*:Office\\:5G\n"); ssid != "Office:5G" {
		t.Fatalf("SSID 解析错误: %q", ssid)
	}
}

func TestParseCIDR(t *testing.T) {
	address, mask := parseCIDR("192.168.10.11/24")
	if address != "192.168.10.11" || mask != "255.255.255.0" {
		t.Fatalf("CIDR 解析错误: address=%q mask=%q", address, mask)
	}
}

func TestParseConnectionIdentity(t *testing.T) {
	identity := parseConnectionIdentity("GENERAL.CONNECTION:Office\\:5G\nGENERAL.CON-UUID:12345678-1234-1234-1234-123456789abc\n")
	if identity.Name != "Office:5G" || identity.UUID != "12345678-1234-1234-1234-123456789abc" {
		t.Fatalf("活动连接身份解析错误: %#v", identity)
	}
}

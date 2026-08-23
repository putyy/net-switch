package rule

import (
	"errors"
	"reflect"
	"testing"

	config2 "github.com/putyy/net-switch/internal/config"
)

func TestManagerCRUD(t *testing.T) {
	store := &memoryStore{}
	manager := newTestManager(t, store, config2.Default())

	created, err := manager.Create(dhcpInput("公司网络", "Office-WiFi", true))
	if err != nil {
		t.Fatalf("创建规则失败: %v", err)
	}
	if created.ID != "rule-test-id" {
		t.Fatalf("规则 ID 为 %q", created.ID)
	}

	updated, err := manager.Update(created.ID, staticInput("公司静态网络", "Office-WiFi", true))
	if err != nil {
		t.Fatalf("编辑规则失败: %v", err)
	}
	if updated.Name != "公司静态网络" || updated.IPv4.Mode != config2.IPv4Static {
		t.Fatalf("编辑结果不正确: %#v", updated)
	}

	disabled, err := manager.Disable(created.ID)
	if err != nil || disabled.Enabled {
		t.Fatalf("停用规则失败: %#v, %v", disabled, err)
	}
	enabled, err := manager.Enable(created.ID)
	if err != nil || !enabled.Enabled {
		t.Fatalf("启用规则失败: %#v, %v", enabled, err)
	}

	if err := manager.Delete(created.ID); err != nil {
		t.Fatalf("删除规则失败: %v", err)
	}
	if rules := manager.List(); len(rules) != 0 {
		t.Fatalf("删除后仍有规则: %#v", rules)
	}
	if len(store.saved) != 5 {
		t.Fatalf("保存次数为 %d，预期 5", len(store.saved))
	}
}

func TestMatchSSIDUsesExactEnabledMatch(t *testing.T) {
	initial := config2.Default()
	initial.Rules = []config2.Rule{
		configuredDHCPRule("office-upper", "Office-WiFi", true),
		configuredDHCPRule("office-lower", "office-wifi", true),
		configuredDHCPRule("guest-disabled", "Guest-WiFi", false),
	}
	manager := newTestManager(t, &memoryStore{}, initial)

	matched, ok := manager.MatchSSID("Office-WiFi")
	if !ok || matched.ID != "office-upper" {
		t.Fatalf("精确匹配结果不正确: %#v, %t", matched, ok)
	}
	matched, ok = manager.MatchSSID("office-wifi")
	if !ok || matched.ID != "office-lower" {
		t.Fatalf("大小写匹配结果不正确: %#v, %t", matched, ok)
	}
	if _, ok := manager.MatchSSID("OFFICE-WIFI"); ok {
		t.Fatal("SSID 不应忽略大小写")
	}
	if _, ok := manager.MatchSSID("Guest-WiFi"); ok {
		t.Fatal("不应匹配已停用规则")
	}
	if _, ok := manager.MatchSSID("Office-WiFi "); ok {
		t.Fatal("SSID 不应自动去除空格")
	}
}

func TestManagerRejectsEnabledSSIDConflict(t *testing.T) {
	initial := config2.Default()
	initial.Rules = []config2.Rule{configuredDHCPRule("existing", "Office-WiFi", true)}
	store := &memoryStore{}
	manager := newTestManager(t, store, initial)

	_, err := manager.Create(dhcpInput("重复网络", "Office-WiFi", true))
	if err == nil {
		t.Fatal("预期重复启用 SSID 创建失败")
	}
	var validationErr *config2.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("预期 ValidationError，得到 %T: %v", err, err)
	}
	if len(store.saved) != 0 {
		t.Fatal("校验失败不应保存配置")
	}
	if rules := manager.List(); len(rules) != 1 || rules[0].ID != "existing" {
		t.Fatalf("校验失败后内存配置发生变化: %#v", rules)
	}
}

func TestManagerKeepsMemoryWhenSaveFails(t *testing.T) {
	store := &memoryStore{saveErr: errors.New("disk full")}
	manager := newTestManager(t, store, config2.Default())

	_, err := manager.Create(dhcpInput("公司网络", "Office-WiFi", true))
	if err == nil {
		t.Fatal("预期保存失败")
	}
	if rules := manager.List(); len(rules) != 0 {
		t.Fatalf("保存失败后内存配置发生变化: %#v", rules)
	}
}

func TestManagerReturnsIndependentCopies(t *testing.T) {
	initial := config2.Default()
	initial.Rules = []config2.Rule{{
		ID:      "static-rule",
		Name:    "静态网络",
		SSID:    "Office-WiFi",
		Enabled: true,
		IPv4: config2.IPv4Config{
			Mode:    config2.IPv4Static,
			Address: "192.168.10.66",
			Netmask: "255.255.255.0",
			Gateway: "192.168.10.1",
			DNS:     []string{"1.1.1.1"},
		},
	}}
	manager := newTestManager(t, &memoryStore{}, initial)

	first := manager.List()
	first[0].Name = "被修改"
	first[0].IPv4.DNS[0] = "8.8.8.8"
	second := manager.List()
	if second[0].Name == first[0].Name || reflect.DeepEqual(second[0].IPv4.DNS, first[0].IPv4.DNS) {
		t.Fatalf("返回值修改影响了内部状态: %#v", second[0])
	}
}

func TestManagerUpdatesGeneralSettings(t *testing.T) {
	store := &memoryStore{}
	manager := newTestManager(t, store, config2.Default())
	wanted := config2.GeneralSettings{AutoSwitch: false, UnmatchedAction: config2.UnmatchedDHCP, Language: config2.LanguageEnglish}

	updated, err := manager.UpdateGeneral(wanted)
	if err != nil {
		t.Fatalf("更新通用设置失败: %v", err)
	}
	if updated != wanted || manager.Snapshot().General != wanted {
		t.Fatalf("通用设置不正确: %#v", manager.Snapshot().General)
	}
	if len(store.saved) != 1 {
		t.Fatalf("保存次数为 %d，预期 1", len(store.saved))
	}
}

func TestManagerReturnsNotFound(t *testing.T) {
	manager := newTestManager(t, &memoryStore{}, config2.Default())
	if _, err := manager.Get("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get 错误为 %v", err)
	}
	if _, err := manager.Update("missing", dhcpInput("网络", "SSID", true)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update 错误为 %v", err)
	}
	if err := manager.Delete("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete 错误为 %v", err)
	}
}

type memoryStore struct {
	saved   []config2.Config
	saveErr error
}

func (s *memoryStore) Save(configuration config2.Config) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = append(s.saved, configuration)
	return nil
}

func newTestManager(t *testing.T, store Store, initial config2.Config) *Manager {
	t.Helper()
	manager, err := NewManager(store, initial)
	if err != nil {
		t.Fatalf("创建规则管理器失败: %v", err)
	}
	manager.generateID = func() (string, error) {
		return "rule-test-id", nil
	}
	return manager
}

func dhcpInput(name, ssid string, enabled bool) Input {
	return Input{
		Name:    name,
		SSID:    ssid,
		Enabled: enabled,
		IPv4:    config2.IPv4Config{Mode: config2.IPv4DHCP},
	}
}

func staticInput(name, ssid string, enabled bool) Input {
	return Input{
		Name:    name,
		SSID:    ssid,
		Enabled: enabled,
		IPv4: config2.IPv4Config{
			Mode:    config2.IPv4Static,
			Address: "192.168.10.66",
			Netmask: "255.255.255.0",
			Gateway: "192.168.10.1",
			DNS:     []string{"1.1.1.1"},
		},
	}
}

func configuredDHCPRule(id, ssid string, enabled bool) config2.Rule {
	return config2.Rule{
		ID:      id,
		Name:    id,
		SSID:    ssid,
		Enabled: enabled,
		IPv4:    config2.IPv4Config{Mode: config2.IPv4DHCP},
	}
}

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/putyy/net-switch/internal/config"
	network2 "github.com/putyy/net-switch/internal/network"
	"github.com/putyy/net-switch/internal/rule"
)

const (
	testHost  = "127.0.0.1:43210"
	testToken = "test-session-token"
)

func TestHandlerAcceptsOnlyConfiguredHost(t *testing.T) {
	handler := testHandler(t)
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:43210/", nil)
	request.Host = "attacker.example"
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("状态码 = %d，期望 %d", response.Code, http.StatusForbidden)
	}
}

func TestStaticHandlerRejectsStateChangingMethods(t *testing.T) {
	handler := testHandler(t)
	request := httptest.NewRequest(http.MethodPost, "http://"+testHost+"/", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 = %d，期望 %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func TestAPIRequiresSessionToken(t *testing.T) {
	handler := testHandler(t)
	request := httptest.NewRequest(http.MethodGet, "http://"+testHost+"/api/v1/config", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，期望 %d", response.Code, http.StatusUnauthorized)
	}
}

func TestInfoAPIReturnsVersion(t *testing.T) {
	handler := newHandler(testHost, testToken, testFiles(t), Dependencies{
		Version: "1.2.3",
		Rules:   &fakeRuleManager{configuration: config.Default()},
	})
	request := apiRequest(http.MethodGet, "/api/v1/info", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 %d", response.Code, http.StatusOK)
	}
	var info AppInfo
	if err := json.Unmarshal(response.Body.Bytes(), &info); err != nil {
		t.Fatalf("解析版本响应失败: %v", err)
	}
	if info.Version != "1.2.3" {
		t.Fatalf("版本 = %q，期望 1.2.3", info.Version)
	}
}

func TestAPIRejectsCrossOriginRequest(t *testing.T) {
	handler := testHandler(t)
	request := apiRequest(http.MethodGet, "/api/v1/config", nil)
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("状态码 = %d，期望 %d", response.Code, http.StatusForbidden)
	}
}

func TestAPIRequiresOriginForMutation(t *testing.T) {
	handler := testHandler(t)
	request := apiRequest(http.MethodPost, "/api/v1/rules", strings.NewReader(`{}`))
	request.Header.Del("Origin")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusForbidden {
		t.Fatalf("状态码 = %d，期望 %d", response.Code, http.StatusForbidden)
	}
}

func TestAPIRejectsUnknownJSONFields(t *testing.T) {
	handler := testHandler(t)
	request := apiRequest(http.MethodPost, "/api/v1/rules", strings.NewReader(`{"name":"测试","unexpected":true}`))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 %d，响应: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestAPIRejectsTrailingJSON(t *testing.T) {
	handler := testHandler(t)
	request := apiRequest(http.MethodPost, "/api/v1/rules", strings.NewReader(`{} {}`))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d，期望 %d，响应: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
}

func TestAPIRejectsUnsupportedMethod(t *testing.T) {
	handler := testHandler(t)
	request := apiRequest(http.MethodPatch, "/api/v1/rules", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("状态码 = %d，期望 %d，响应: %s", response.Code, http.StatusMethodNotAllowed, response.Body.String())
	}
}

func TestAPIRejectsOversizedJSON(t *testing.T) {
	handler := testHandler(t)
	body := `{"name":"` + strings.Repeat("a", maxJSONBodySize) + `"}`
	request := apiRequest(http.MethodPost, "/api/v1/rules", strings.NewReader(body))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("状态码 = %d，期望 %d，响应: %s", response.Code, http.StatusRequestEntityTooLarge, response.Body.String())
	}
}

func TestRuleAPIUsesManager(t *testing.T) {
	manager := &fakeRuleManager{configuration: config.Default()}
	handler := newHandler(testHost, testToken, testFiles(t), Dependencies{Rules: manager})
	input := rule.Input{
		Name:    "公司网络",
		SSID:    "Office-WiFi",
		Enabled: true,
		IPv4:    config.IPv4Config{Mode: config.IPv4DHCP},
	}
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("编码请求失败: %v", err)
	}
	request := apiRequest(http.MethodPost, "/api/v1/rules", bytes.NewReader(body))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("状态码 = %d，期望 %d，响应: %s", response.Code, http.StatusCreated, response.Body.String())
	}
	if manager.created.SSID != input.SSID {
		t.Fatalf("规则管理器收到的输入不正确: %#v", manager.created)
	}
}

func TestStateAPIUsesProvider(t *testing.T) {
	wanted := network2.RuntimeState{
		Network: network2.State{SSID: "Office-WiFi", Mode: network2.AddressModeDHCP},
		TargetComparison: &network2.ConfigurationComparison{
			Comparable: true,
			Matches:    true,
		},
	}
	handler := newHandler(testHost, testToken, testFiles(t), Dependencies{
		Rules: &fakeRuleManager{configuration: config.Default()},
		CurrentState: func(context.Context) network2.RuntimeState {
			return wanted
		},
	})
	request := apiRequest(http.MethodGet, "/api/v1/state", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 %d", response.Code, http.StatusOK)
	}
	var received network2.RuntimeState
	if err := json.Unmarshal(response.Body.Bytes(), &received); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if received.Network.SSID != wanted.Network.SSID || received.TargetComparison == nil || !received.TargetComparison.Matches {
		t.Fatalf("状态响应不正确: %#v", received)
	}
}

func TestSettingsAPIInvokesUpdateCallback(t *testing.T) {
	manager := &fakeRuleManager{configuration: config.Default()}
	var notified config.GeneralSettings
	handler := newHandler(testHost, testToken, testFiles(t), Dependencies{
		Rules: manager,
		OnSettingsUpdated: func(settings config.GeneralSettings) {
			notified = settings
		},
	})
	request := apiRequest(
		http.MethodPut,
		"/api/v1/settings",
		strings.NewReader(`{"auto_switch":false,"unmatched_action":"keep"}`),
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 %d，响应: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if notified.AutoSwitch || notified.UnmatchedAction != config.UnmatchedKeep || notified.Language != config.LanguageChinese {
		t.Fatalf("设置更新回调内容错误: %#v", notified)
	}
}

func TestSettingsAPIUpdatesLanguage(t *testing.T) {
	manager := &fakeRuleManager{configuration: config.Default()}
	handler := newHandler(testHost, testToken, testFiles(t), Dependencies{Rules: manager})
	request := apiRequest(
		http.MethodPut,
		"/api/v1/settings",
		strings.NewReader(`{"auto_switch":true,"unmatched_action":"keep","language":"en"}`),
	)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 %d，响应: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if manager.configuration.General.Language != config.LanguageEnglish {
		t.Fatalf("语言设置 = %q，期望 %q", manager.configuration.General.Language, config.LanguageEnglish)
	}
}

func TestAutoStartAPIReadsAvailableState(t *testing.T) {
	handler := newHandler(testHost, testToken, testFiles(t), Dependencies{
		Rules: &fakeRuleManager{configuration: config.Default()},
		AutoStartState: func(context.Context) (bool, error) {
			return true, nil
		},
	})
	request := apiRequest(http.MethodGet, "/api/v1/autostart", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 %d", response.Code, http.StatusOK)
	}
	var received AutoStartState
	if err := json.Unmarshal(response.Body.Bytes(), &received); err != nil || !received.Available || !received.Enabled {
		t.Fatalf("开机启动状态响应错误: %#v, %v", received, err)
	}
}

func TestAutoStartAPIUpdatesAndReturnsActualState(t *testing.T) {
	requested := false
	handler := newHandler(testHost, testToken, testFiles(t), Dependencies{
		Rules: &fakeRuleManager{configuration: config.Default()},
		SetAutoStart: func(_ context.Context, enabled bool) (bool, error) {
			requested = enabled
			return enabled, nil
		},
	})
	request := apiRequest(http.MethodPut, "/api/v1/autostart", strings.NewReader(`{"enabled":true}`))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !requested {
		t.Fatalf("开机启动更新响应错误: status=%d requested=%t body=%s", response.Code, requested, response.Body.String())
	}
	var received AutoStartState
	if err := json.Unmarshal(response.Body.Bytes(), &received); err != nil || !received.Available || !received.Enabled {
		t.Fatalf("开机启动更新结果错误: %#v, %v", received, err)
	}
}

func TestAutoStartAPIReportsUnavailableState(t *testing.T) {
	handler := testHandler(t)
	request := apiRequest(http.MethodGet, "/api/v1/autostart", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"available":false`) {
		t.Fatalf("不可用状态响应错误: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestLogsAPIUsesBoundedLimit(t *testing.T) {
	receivedLimit := 0
	handler := newHandler(testHost, testToken, testFiles(t), Dependencies{
		Rules: &fakeRuleManager{configuration: config.Default()},
		RecentLogs: func(_ context.Context, limit int) ([]string, error) {
			receivedLimit = limit
			return []string{"first", "second"}, nil
		},
	})
	request := apiRequest(http.MethodGet, "/api/v1/logs?limit=2", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || receivedLimit != 2 {
		t.Fatalf("日志响应错误: status=%d limit=%d body=%s", response.Code, receivedLimit, response.Body.String())
	}
	var received LogResponse
	if err := json.Unmarshal(response.Body.Bytes(), &received); err != nil || len(received.Entries) != 2 {
		t.Fatalf("日志响应内容错误: %#v, %v", received, err)
	}
}

func TestLogsAPIRejectsInvalidLimit(t *testing.T) {
	handler := newHandler(testHost, testToken, testFiles(t), Dependencies{
		Rules:      &fakeRuleManager{configuration: config.Default()},
		RecentLogs: func(context.Context, int) ([]string, error) { return nil, nil },
	})
	request := apiRequest(http.MethodGet, "/api/v1/logs?limit=201", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_query") {
		t.Fatalf("无效日志行数响应错误: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestLogsAPIReportsUnavailableWithoutProvider(t *testing.T) {
	handler := testHandler(t)
	request := apiRequest(http.MethodGet, "/api/v1/logs", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "logs_unavailable") {
		t.Fatalf("日志不可用响应错误: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNetworkApplyAPIInvokesOnlyConfiguredAction(t *testing.T) {
	called := false
	wanted := network2.OperationResult{
		Action:  network2.OperationApplyRule,
		Success: true,
		Message: "已应用",
	}
	handler := newHandler(testHost, testToken, testFiles(t), Dependencies{
		Rules: &fakeRuleManager{configuration: config.Default()},
		ApplyMatchedRule: func(context.Context) (network2.OperationResult, error) {
			called = true
			return wanted, nil
		},
	})
	request := apiRequest(http.MethodPost, "/api/v1/network/apply", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || !called {
		t.Fatalf("立即应用响应错误: status=%d called=%t body=%s", response.Code, called, response.Body.String())
	}
	var received network2.OperationResult
	if err := json.Unmarshal(response.Body.Bytes(), &received); err != nil || received.Action != wanted.Action || !received.Success {
		t.Fatalf("立即应用结果错误: %#v, %v", received, err)
	}
}

func TestRestoreDHCPAPIReportsUnavailableNetwork(t *testing.T) {
	handler := newHandler(testHost, testToken, testFiles(t), Dependencies{
		Rules: &fakeRuleManager{configuration: config.Default()},
		RestoreDHCP: func(context.Context) (network2.OperationResult, error) {
			result := network2.OperationResult{Action: network2.OperationRestoreDHCP, Message: "当前没有 Wi-Fi 网络服务"}
			return result, network2.ErrNetworkUnavailable
		},
	})
	request := apiRequest(http.MethodPost, "/api/v1/network/restore-dhcp", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "network_unavailable") {
		t.Fatalf("恢复 DHCP 错误响应不正确: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestNetworkOperationAPIIsUnavailableWithoutCallback(t *testing.T) {
	handler := testHandler(t)
	request := apiRequest(http.MethodPost, "/api/v1/network/apply", nil)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("未配置网络操作时状态码 = %d，期望 %d", response.Code, http.StatusServiceUnavailable)
	}
}

func apiRequest(method, path string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, "http://"+testHost+path, body)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set(SessionHeader, testToken)
	if changesState(method) {
		request.Header.Set("Origin", "http://"+testHost)
	}
	return request
}

func testHandler(t *testing.T) http.Handler {
	t.Helper()
	return newHandler(testHost, testToken, testFiles(t), Dependencies{
		Rules: &fakeRuleManager{configuration: config.Default()},
	})
}

func testFiles(t *testing.T) fs.FS {
	t.Helper()
	return fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><title>Net Switch</title>")},
	}
}

type fakeRuleManager struct {
	configuration config.Config
	created       rule.Input
}

func (m *fakeRuleManager) Snapshot() config.Config {
	return m.configuration
}

func (m *fakeRuleManager) List() []config.Rule {
	return m.configuration.Rules
}

func (m *fakeRuleManager) Get(id string) (config.Rule, error) {
	for _, configuredRule := range m.configuration.Rules {
		if configuredRule.ID == id {
			return configuredRule, nil
		}
	}
	return config.Rule{}, rule.ErrNotFound
}

func (m *fakeRuleManager) Create(input rule.Input) (config.Rule, error) {
	m.created = input
	created := config.Rule{ID: "rule-created", Name: input.Name, SSID: input.SSID, Enabled: input.Enabled, IPv4: input.IPv4}
	m.configuration.Rules = append(m.configuration.Rules, created)
	return created, nil
}

func (m *fakeRuleManager) Update(id string, input rule.Input) (config.Rule, error) {
	return config.Rule{ID: id, Name: input.Name, SSID: input.SSID, Enabled: input.Enabled, IPv4: input.IPv4}, nil
}

func (m *fakeRuleManager) Delete(string) error {
	return nil
}

func (m *fakeRuleManager) Enable(id string) (config.Rule, error) {
	return config.Rule{ID: id, Enabled: true}, nil
}

func (m *fakeRuleManager) Disable(id string) (config.Rule, error) {
	return config.Rule{ID: id, Enabled: false}, nil
}

func (m *fakeRuleManager) UpdateGeneral(settings config.GeneralSettings) (config.GeneralSettings, error) {
	m.configuration.General = settings
	return settings, nil
}

var _ RuleManager = (*fakeRuleManager)(nil)

func TestBusinessErrorHidesInternalDetails(t *testing.T) {
	response := httptest.NewRecorder()
	handleBusinessError(response, errors.New("secret path"))
	if strings.Contains(response.Body.String(), "secret path") {
		t.Fatal("内部错误详情不应返回给页面")
	}
}

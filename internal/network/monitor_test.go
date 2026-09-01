package network

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeReader struct {
	state State
	err   error
}

func (r fakeReader) Read(context.Context) (State, error) {
	return r.state, r.err
}

type fakeWatcher struct {
	events chan struct{}
}

type mutableReader struct {
	mu    sync.RWMutex
	state State
}

func (r *mutableReader) Read(context.Context) (State, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state, nil
}

func (r *mutableReader) setState(state State) {
	r.mu.Lock()
	r.state = state
	r.mu.Unlock()
}

func (w fakeWatcher) Events() <-chan struct{} { return w.events }
func (w fakeWatcher) Close() error            { return nil }

func TestNewMonitorRejectsInvalidOptions(t *testing.T) {
	_, err := NewMonitor(fakeReader{}, nil, MonitorOptions{})
	if err == nil {
		t.Fatal("预期拒绝无效监控参数")
	}
}

func TestMonitorPublishesInitialState(t *testing.T) {
	monitor, err := NewMonitor(fakeReader{state: State{
		Status: StateStatusConnected,
		SSID:   "Office-WiFi",
		Mode:   AddressModeDHCP,
	}}, fakeWatcher{events: make(chan struct{})}, MonitorOptions{
		DebounceDelay:  10 * time.Millisecond,
		ResyncInterval: time.Hour,
		ReadTimeout:    time.Second,
	})
	if err != nil {
		t.Fatalf("创建监控器失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		monitor.Run(ctx)
		close(done)
	}()

	select {
	case update := <-monitor.Updates():
		if update.Err != nil || update.State.SSID != "Office-WiFi" {
			t.Fatalf("初始状态错误: %#v, %v", update.State, update.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到初始网络状态")
	}
	cancel()
	<-done
}

func TestNormalizeStatePreservesReadableError(t *testing.T) {
	state := normalizeState(State{}, errors.New("read failed"))
	if state.Status != StateStatusUnavailable || state.Message == "" || state.Mode != AddressModeUnknown || state.DNSMode != DNSModeUnknown || state.DNS == nil {
		t.Fatalf("错误状态未规范化: %#v", state)
	}
}

func TestMonitorRefreshUpdatesSnapshot(t *testing.T) {
	reader := &mutableReader{state: State{
		Status:  StateStatusConnected,
		SSID:    "Office-WiFi",
		Mode:    AddressModeDHCP,
		DNSMode: DNSModeAutomatic,
	}}
	monitor, err := NewMonitor(reader, nil, MonitorOptions{
		DebounceDelay:  time.Millisecond,
		ResyncInterval: time.Hour,
		ReadTimeout:    time.Second,
	})
	if err != nil {
		t.Fatalf("创建监控器失败: %v", err)
	}

	update := monitor.Refresh(context.Background())
	if update.Err != nil || update.State.SSID != "Office-WiFi" || monitor.Snapshot().SSID != "Office-WiFi" {
		t.Fatalf("主动刷新结果错误: %#v, %v", update.State, update.Err)
	}
}

func TestMonitorRetriesDisconnectedInitialState(t *testing.T) {
	reader := &mutableReader{state: State{Status: StateStatusDisconnected}}
	monitor, err := NewMonitor(reader, nil, MonitorOptions{
		DebounceDelay:  time.Millisecond,
		ResyncInterval: time.Hour,
		ReadTimeout:    time.Second,
		RetryDelays:    []time.Duration{20 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("创建监控器失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go monitor.Run(ctx)

	select {
	case update := <-monitor.Updates():
		if update.State.Status != StateStatusDisconnected {
			t.Fatalf("初始状态错误: %#v", update.State)
		}
	case <-time.After(time.Second):
		t.Fatal("未收到初始断网状态")
	}

	reader.setState(State{Status: StateStatusConnected, SSID: "Office-WiFi", Mode: AddressModeDHCP})
	select {
	case update := <-monitor.Updates():
		if update.State.Status != StateStatusConnected || update.State.SSID != "Office-WiFi" {
			t.Fatalf("重试后的状态错误: %#v", update.State)
		}
	case <-time.After(time.Second):
		t.Fatal("初始断网后未自动重试")
	}
}

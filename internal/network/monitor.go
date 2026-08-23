package network

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"time"
)

var defaultReadRetryDelays = []time.Duration{
	time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
}

type Reader interface {
	Read(context.Context) (State, error)
}

type ChangeWatcher interface {
	Events() <-chan struct{}
	Close() error
}

type MonitorOptions struct {
	DebounceDelay  time.Duration
	ResyncInterval time.Duration
	ReadTimeout    time.Duration
	RetryDelays    []time.Duration
}

type Update struct {
	State State
	Err   error
}

type Monitor struct {
	reader  Reader
	watcher ChangeWatcher
	options MonitorOptions

	mu        sync.RWMutex
	refreshMu sync.Mutex
	state     State
	updates   chan Update
}

func NewMonitor(reader Reader, watcher ChangeWatcher, options MonitorOptions) (*Monitor, error) {
	if reader == nil {
		return nil, errors.New("network state reader is required")
	}
	if options.DebounceDelay <= 0 {
		return nil, errors.New("network event debounce delay must be positive")
	}
	if options.ResyncInterval <= 0 {
		return nil, errors.New("network synchronization interval must be positive")
	}
	if options.ReadTimeout <= 0 {
		return nil, errors.New("network read timeout must be positive")
	}
	if len(options.RetryDelays) == 0 {
		options.RetryDelays = defaultReadRetryDelays
	}
	for _, delay := range options.RetryDelays {
		if delay <= 0 {
			return nil, errors.New("network read retry delay must be positive")
		}
	}
	options.RetryDelays = append([]time.Duration(nil), options.RetryDelays...)
	return &Monitor{
		reader:  reader,
		watcher: watcher,
		options: options,
		state: State{
			Status:     StateStatusUnknown,
			Message:    "Reading network status",
			MessageKey: "state.reading",
			Mode:       AddressModeUnknown,
			DNSMode:    DNSModeUnknown,
			DNS:        []string{},
		},
		updates: make(chan Update, 1),
	}, nil
}

func (m *Monitor) Run(ctx context.Context) {
	initialUpdate := m.refresh(ctx)

	resyncTicker := time.NewTicker(m.options.ResyncInterval)
	defer resyncTicker.Stop()

	debounceTimer := time.NewTimer(m.options.DebounceDelay)
	if !debounceTimer.Stop() {
		<-debounceTimer.C
	}
	defer debounceTimer.Stop()
	var debounce <-chan time.Time
	retryTimer := time.NewTimer(m.options.RetryDelays[0])
	if !retryTimer.Stop() {
		<-retryTimer.C
	}
	defer retryTimer.Stop()
	var retry <-chan time.Time
	retryAttempt := 0
	stopRetry := func(resetAttempts bool) {
		if !retryTimer.Stop() && retry != nil {
			select {
			case <-retryTimer.C:
			default:
			}
		}
		retry = nil
		if resetAttempts {
			retryAttempt = 0
		}
	}
	scheduleRetry := func(update Update, resetAttempts bool) {
		if !shouldRetryRead(update) {
			stopRetry(true)
			return
		}
		stopRetry(resetAttempts)
		if retryAttempt >= len(m.options.RetryDelays) {
			return
		}
		retryTimer.Reset(m.options.RetryDelays[retryAttempt])
		retryAttempt++
		retry = retryTimer.C
	}
	scheduleRetry(initialUpdate, true)

	var events <-chan struct{}
	if m.watcher != nil {
		events = m.watcher.Events()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			stopRetry(true)
			if !debounceTimer.Stop() && debounce != nil {
				select {
				case <-debounceTimer.C:
				default:
				}
			}
			debounceTimer.Reset(m.options.DebounceDelay)
			debounce = debounceTimer.C
		case <-debounce:
			debounce = nil
			scheduleRetry(m.refresh(ctx), true)
		case <-retry:
			retry = nil
			scheduleRetry(m.refresh(ctx), false)
		case <-resyncTicker.C:
			scheduleRetry(m.refresh(ctx), true)
		}
	}
}

func shouldRetryRead(update Update) bool {
	return update.Err != nil || update.State.Status == StateStatusUnavailable || update.State.Status == StateStatusUnknown
}

func (m *Monitor) Snapshot() State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneState(m.state)
}

func (m *Monitor) Updates() <-chan Update {
	return m.updates
}

func (m *Monitor) Refresh(ctx context.Context) Update {
	return m.refresh(ctx)
}

func (m *Monitor) refresh(ctx context.Context) Update {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()

	readCtx, cancel := context.WithTimeout(ctx, m.options.ReadTimeout)
	defer cancel()

	state, err := m.reader.Read(readCtx)
	state = normalizeState(state, err)
	update := Update{State: cloneState(state), Err: err}

	m.mu.Lock()
	changed := !reflect.DeepEqual(m.state, state)
	m.state = cloneState(state)
	m.mu.Unlock()

	if !changed && err == nil {
		return update
	}
	m.publish(update)
	return update
}

func (m *Monitor) publish(update Update) {
	select {
	case m.updates <- update:
		return
	default:
	}
	select {
	case <-m.updates:
	default:
	}
	select {
	case m.updates <- update:
	default:
	}
}

func normalizeState(state State, readErr error) State {
	if state.Status == "" {
		if readErr != nil {
			state.Status = StateStatusUnavailable
		} else {
			state.Status = StateStatusUnknown
		}
	}
	if state.Mode == "" {
		state.Mode = AddressModeUnknown
	}
	if state.DNSMode == "" {
		state.DNSMode = DNSModeUnknown
	}
	if state.DNS == nil {
		state.DNS = []string{}
	}
	if readErr != nil && state.Message == "" {
		state.Message = "Network status is temporarily unavailable"
		state.MessageKey = "state.unavailable"
	}
	return state
}

func cloneState(state State) State {
	state.DNS = append([]string(nil), state.DNS...)
	if state.DNS == nil {
		state.DNS = []string{}
	}
	return state
}

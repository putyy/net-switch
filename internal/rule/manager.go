package rule

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/putyy/net-switch/internal/config"
)

const (
	ruleIDPrefix      = "rule-"
	ruleIDRandomBytes = 8
	maxIDAttempts     = 10
)

var ErrNotFound = errors.New("rule not found")

type Store interface {
	Save(config.Config) error
}

type Input struct {
	Name    string            `json:"name"`
	SSID    string            `json:"ssid"`
	Enabled bool              `json:"enabled"`
	IPv4    config.IPv4Config `json:"ipv4"`
}

type Manager struct {
	mu            sync.RWMutex
	store         Store
	configuration config.Config
	generateID    func() (string, error)
}

func NewManager(store Store, initial config.Config) (*Manager, error) {
	if store == nil {
		return nil, errors.New("configuration storage is required")
	}
	if err := initial.Validate(); err != nil {
		return nil, fmt.Errorf("invalid initial configuration: %w", err)
	}
	return &Manager{
		store:         store,
		configuration: cloneConfiguration(initial),
		generateID:    randomRuleID,
	}, nil
}

func (m *Manager) Snapshot() config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneConfiguration(m.configuration)
}

func (m *Manager) List() []config.Rule {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneRules(m.configuration.Rules)
}

func (m *Manager) UpdateGeneral(settings config.GeneralSettings) (config.GeneralSettings, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	candidate := cloneConfiguration(m.configuration)
	candidate.General = settings
	if err := m.persistLocked(candidate); err != nil {
		return config.GeneralSettings{}, err
	}
	return candidate.General, nil
}

func (m *Manager) Get(id string) (config.Rule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	index := findRule(m.configuration.Rules, id)
	if index < 0 {
		return config.Rule{}, notFound(id)
	}
	return cloneRule(m.configuration.Rules[index]), nil
}

func (m *Manager) Create(input Input) (config.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	id, err := m.uniqueIDLocked()
	if err != nil {
		return config.Rule{}, err
	}
	created := config.Rule{
		ID:      id,
		Name:    input.Name,
		SSID:    input.SSID,
		Enabled: input.Enabled,
		IPv4:    cloneIPv4(input.IPv4),
	}
	candidate := cloneConfiguration(m.configuration)
	candidate.Rules = append(candidate.Rules, created)
	if err := m.persistLocked(candidate); err != nil {
		return config.Rule{}, err
	}
	return cloneRule(created), nil
}

func (m *Manager) Update(id string, input Input) (config.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	candidate := cloneConfiguration(m.configuration)
	index := findRule(candidate.Rules, id)
	if index < 0 {
		return config.Rule{}, notFound(id)
	}
	updated := config.Rule{
		ID:      id,
		Name:    input.Name,
		SSID:    input.SSID,
		Enabled: input.Enabled,
		IPv4:    cloneIPv4(input.IPv4),
	}
	candidate.Rules[index] = updated
	if err := m.persistLocked(candidate); err != nil {
		return config.Rule{}, err
	}
	return cloneRule(updated), nil
}

func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	candidate := cloneConfiguration(m.configuration)
	index := findRule(candidate.Rules, id)
	if index < 0 {
		return notFound(id)
	}
	candidate.Rules = append(candidate.Rules[:index], candidate.Rules[index+1:]...)
	return m.persistLocked(candidate)
}

func (m *Manager) Enable(id string) (config.Rule, error) {
	return m.setEnabled(id, true)
}

func (m *Manager) Disable(id string) (config.Rule, error) {
	return m.setEnabled(id, false)
}

func (m *Manager) MatchSSID(ssid string) (config.Rule, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, configuredRule := range m.configuration.Rules {
		if configuredRule.Enabled && configuredRule.SSID == ssid {
			return cloneRule(configuredRule), true
		}
	}
	return config.Rule{}, false
}

func (m *Manager) setEnabled(id string, enabled bool) (config.Rule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	candidate := cloneConfiguration(m.configuration)
	index := findRule(candidate.Rules, id)
	if index < 0 {
		return config.Rule{}, notFound(id)
	}
	if candidate.Rules[index].Enabled == enabled {
		return cloneRule(candidate.Rules[index]), nil
	}
	candidate.Rules[index].Enabled = enabled
	if err := m.persistLocked(candidate); err != nil {
		return config.Rule{}, err
	}
	return cloneRule(candidate.Rules[index]), nil
}

func (m *Manager) persistLocked(candidate config.Config) error {
	if err := candidate.Validate(); err != nil {
		return fmt.Errorf("validate rule change: %w", err)
	}
	if err := m.store.Save(candidate); err != nil {
		return fmt.Errorf("save rule change: %w", err)
	}
	m.configuration = candidate
	return nil
}

func (m *Manager) uniqueIDLocked() (string, error) {
	for attempt := 0; attempt < maxIDAttempts; attempt++ {
		id, err := m.generateID()
		if err != nil {
			return "", fmt.Errorf("generate rule ID: %w", err)
		}
		if findRule(m.configuration.Rules, id) < 0 {
			return id, nil
		}
	}
	return "", errors.New("generated rule ID collided repeatedly")
}

func randomRuleID() (string, error) {
	randomBytes := make([]byte, ruleIDRandomBytes)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}
	return ruleIDPrefix + hex.EncodeToString(randomBytes), nil
}

func findRule(rules []config.Rule, id string) int {
	for index := range rules {
		if rules[index].ID == id {
			return index
		}
	}
	return -1
}

func cloneConfiguration(source config.Config) config.Config {
	return config.Config{
		General: source.General,
		Rules:   cloneRules(source.Rules),
	}
}

func cloneRules(source []config.Rule) []config.Rule {
	if source == nil {
		return nil
	}
	cloned := make([]config.Rule, len(source))
	for index := range source {
		cloned[index] = cloneRule(source[index])
	}
	return cloned
}

func cloneRule(source config.Rule) config.Rule {
	source.IPv4 = cloneIPv4(source.IPv4)
	return source
}

func cloneIPv4(source config.IPv4Config) config.IPv4Config {
	if source.DNS != nil {
		source.DNS = append([]string(nil), source.DNS...)
	}
	return source
}

func notFound(id string) error {
	return fmt.Errorf("%w: %s", ErrNotFound, id)
}

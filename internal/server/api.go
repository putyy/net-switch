package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"strconv"

	config2 "github.com/putyy/net-switch/internal/config"
	network2 "github.com/putyy/net-switch/internal/network"
	"github.com/putyy/net-switch/internal/rule"
)

const (
	maxJSONBodySize = 64 * 1024
	defaultLogLimit = 100
	maxLogLimit     = 200
)

type RuleManager interface {
	Snapshot() config2.Config
	List() []config2.Rule
	Get(string) (config2.Rule, error)
	Create(rule.Input) (config2.Rule, error)
	Update(string, rule.Input) (config2.Rule, error)
	Delete(string) error
	Enable(string) (config2.Rule, error)
	Disable(string) (config2.Rule, error)
	UpdateGeneral(config2.GeneralSettings) (config2.GeneralSettings, error)
}

type Dependencies struct {
	Version           string
	Rules             RuleManager
	CurrentState      func(context.Context) network2.RuntimeState
	OnSettingsUpdated func(config2.GeneralSettings)
	ApplyMatchedRule  func(context.Context) (network2.OperationResult, error)
	RestoreDHCP       func(context.Context) (network2.OperationResult, error)
	AutoStartState    func(context.Context) (bool, error)
	SetAutoStart      func(context.Context, bool) (bool, error)
	RecentLogs        func(context.Context, int) ([]string, error)
}

type api struct {
	version           string
	rules             RuleManager
	currentState      func(context.Context) network2.RuntimeState
	onSettingsUpdated func(config2.GeneralSettings)
	applyMatchedRule  func(context.Context) (network2.OperationResult, error)
	restoreDHCP       func(context.Context) (network2.OperationResult, error)
	autoStartState    func(context.Context) (bool, error)
	setAutoStart      func(context.Context, bool) (bool, error)
	recentLogs        func(context.Context, int) ([]string, error)
}

type AutoStartState struct {
	Available  bool   `json:"available"`
	Enabled    bool   `json:"enabled"`
	Message    string `json:"message,omitempty"`
	MessageKey string `json:"message_key,omitempty"`
}

type LogResponse struct {
	Entries []string `json:"entries"`
}

type AppInfo struct {
	Version string `json:"version"`
}

type enabledRequest struct {
	Enabled *bool `json:"enabled"`
}

type settingsRequest struct {
	AutoSwitch      *bool                    `json:"auto_switch"`
	UnmatchedAction *config2.UnmatchedAction `json:"unmatched_action"`
	Language        *config2.Language        `json:"language"`
}

type apiErrorEnvelope struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

func newAPIHandler(dependencies Dependencies) http.Handler {
	stateProvider := dependencies.CurrentState
	if stateProvider == nil {
		stateProvider = func(context.Context) network2.RuntimeState {
			return network2.RuntimeState{Network: network2.State{Status: network2.StateStatusUnknown, Mode: network2.AddressModeUnknown}}
		}
	}
	handler := &api{
		version:           dependencies.Version,
		rules:             dependencies.Rules,
		currentState:      stateProvider,
		onSettingsUpdated: dependencies.OnSettingsUpdated,
		applyMatchedRule:  dependencies.ApplyMatchedRule,
		restoreDHCP:       dependencies.RestoreDHCP,
		autoStartState:    dependencies.AutoStartState,
		setAutoStart:      dependencies.SetAutoStart,
		recentLogs:        dependencies.RecentLogs,
	}
	router := http.NewServeMux()
	router.HandleFunc("GET /api/v1/info", handler.getInfo)
	router.HandleFunc("GET /api/v1/state", handler.getState)
	router.HandleFunc("GET /api/v1/config", handler.getConfig)
	router.HandleFunc("GET /api/v1/rules", handler.listRules)
	router.HandleFunc("POST /api/v1/rules", handler.createRule)
	router.HandleFunc("GET /api/v1/rules/{id}", handler.getRule)
	router.HandleFunc("PUT /api/v1/rules/{id}", handler.updateRule)
	router.HandleFunc("DELETE /api/v1/rules/{id}", handler.deleteRule)
	router.HandleFunc("PUT /api/v1/rules/{id}/enabled", handler.setRuleEnabled)
	router.HandleFunc("PUT /api/v1/settings", handler.updateSettings)
	router.HandleFunc("GET /api/v1/autostart", handler.getAutoStart)
	router.HandleFunc("PUT /api/v1/autostart", handler.updateAutoStart)
	router.HandleFunc("GET /api/v1/logs", handler.getLogs)
	router.HandleFunc("POST /api/v1/network/apply", handler.applyCurrentRule)
	router.HandleFunc("POST /api/v1/network/restore-dhcp", handler.restoreCurrentDHCP)
	return router
}

func (a *api) getInfo(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, AppInfo{Version: a.version})
}

func (a *api) getLogs(response http.ResponseWriter, request *http.Request) {
	if a.recentLogs == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "logs_unavailable", "File logging is temporarily unavailable", "")
		return
	}
	limit, err := parseLogLimit(request)
	if err != nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_query", err.Error(), "limit")
		return
	}
	entries, err := a.recentLogs(request.Context(), limit)
	if err != nil {
		log.Printf("Could not read recent logs: %v", err)
		writeAPIError(response, http.StatusInternalServerError, "logs_read_failed", "Could not read recent logs; check the application log file", "")
		return
	}
	if entries == nil {
		entries = []string{}
	}
	writeJSON(response, http.StatusOK, LogResponse{Entries: entries})
}

func parseLogLimit(request *http.Request) (int, error) {
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return 0, errors.New("invalid query encoding")
	}
	for key := range query {
		if key != "limit" {
			return 0, fmt.Errorf("unsupported query parameter %q", key)
		}
	}
	values, ok := query["limit"]
	if !ok {
		return defaultLogLimit, nil
	}
	if len(values) != 1 || values[0] == "" {
		return 0, errors.New("limit must be a single integer")
	}
	limit, err := strconv.Atoi(values[0])
	if err != nil || limit < 1 || limit > maxLogLimit {
		return 0, fmt.Errorf("limit must be between 1 and %d", maxLogLimit)
	}
	return limit, nil
}

func (a *api) getAutoStart(response http.ResponseWriter, request *http.Request) {
	if a.autoStartState == nil {
		writeJSON(response, http.StatusOK, AutoStartState{
			Message:    "Startup management is unavailable on this system",
			MessageKey: "autostart.unavailable",
		})
		return
	}

	enabled, err := a.autoStartState(request.Context())
	if err != nil {
		log.Printf("Could not read start-at-login status: %v", err)
		writeJSON(response, http.StatusOK, AutoStartState{
			Message:    "Start-at-login status is temporarily unavailable",
			MessageKey: "autostart.read_failed",
		})
		return
	}
	writeJSON(response, http.StatusOK, AutoStartState{Available: true, Enabled: enabled})
}

func (a *api) updateAutoStart(response http.ResponseWriter, request *http.Request) {
	if a.setAutoStart == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "autostart_unavailable", "Startup management is unavailable on this system", "")
		return
	}

	var input enabledRequest
	if err := decodeJSON(response, request, &input); err != nil {
		return
	}
	if input.Enabled == nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "enabled is required", "enabled")
		return
	}

	enabled, err := a.setAutoStart(request.Context(), *input.Enabled)
	if err != nil {
		log.Printf("Could not update start-at-login status: %v", err)
		writeAPIError(response, http.StatusInternalServerError, "autostart_update_failed", "Could not update start-at-login status; check the application log", "")
		return
	}
	writeJSON(response, http.StatusOK, AutoStartState{Available: true, Enabled: enabled})
}

func (a *api) applyCurrentRule(response http.ResponseWriter, request *http.Request) {
	a.runNetworkAction(response, request, a.applyMatchedRule)
}

func (a *api) restoreCurrentDHCP(response http.ResponseWriter, request *http.Request) {
	a.runNetworkAction(response, request, a.restoreDHCP)
}

func (a *api) runNetworkAction(response http.ResponseWriter, request *http.Request, action func(context.Context) (network2.OperationResult, error)) {
	if action == nil {
		writeAPIError(response, http.StatusServiceUnavailable, "network_operation_unavailable", "Network operations are temporarily unavailable", "")
		return
	}
	result, err := action(request.Context())
	if err == nil {
		writeJSON(response, http.StatusOK, result)
		return
	}

	message := result.Message
	if message == "" {
		message = "Network operation failed; check the application log"
	}
	switch {
	case errors.Is(err, network2.ErrNoMatchedRule):
		writeAPIError(response, http.StatusConflict, "no_matched_rule", message, "")
	case errors.Is(err, network2.ErrNetworkUnavailable):
		writeAPIError(response, http.StatusConflict, "network_unavailable", message, "")
	default:
		log.Printf("Network operation API failed: %v", err)
		writeAPIError(response, http.StatusInternalServerError, "network_operation_failed", message, "")
	}
}

func (a *api) getState(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, a.currentState(request.Context()))
}

func (a *api) getConfig(response http.ResponseWriter, _ *http.Request) {
	configuration := a.rules.Snapshot()
	if configuration.Rules == nil {
		configuration.Rules = []config2.Rule{}
	}
	writeJSON(response, http.StatusOK, configuration)
}

func (a *api) listRules(response http.ResponseWriter, _ *http.Request) {
	rules := a.rules.List()
	if rules == nil {
		rules = []config2.Rule{}
	}
	writeJSON(response, http.StatusOK, rules)
}

func (a *api) getRule(response http.ResponseWriter, request *http.Request) {
	configuredRule, err := a.rules.Get(request.PathValue("id"))
	if err != nil {
		handleBusinessError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, configuredRule)
}

func (a *api) createRule(response http.ResponseWriter, request *http.Request) {
	var input rule.Input
	if err := decodeJSON(response, request, &input); err != nil {
		return
	}
	created, err := a.rules.Create(input)
	if err != nil {
		handleBusinessError(response, err)
		return
	}
	writeJSON(response, http.StatusCreated, created)
}

func (a *api) updateRule(response http.ResponseWriter, request *http.Request) {
	var input rule.Input
	if err := decodeJSON(response, request, &input); err != nil {
		return
	}
	updated, err := a.rules.Update(request.PathValue("id"), input)
	if err != nil {
		handleBusinessError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, updated)
}

func (a *api) deleteRule(response http.ResponseWriter, request *http.Request) {
	if err := a.rules.Delete(request.PathValue("id")); err != nil {
		handleBusinessError(response, err)
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (a *api) setRuleEnabled(response http.ResponseWriter, request *http.Request) {
	var input enabledRequest
	if err := decodeJSON(response, request, &input); err != nil {
		return
	}
	if input.Enabled == nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "enabled is required", "enabled")
		return
	}

	var (
		updated config2.Rule
		err     error
	)
	if *input.Enabled {
		updated, err = a.rules.Enable(request.PathValue("id"))
	} else {
		updated, err = a.rules.Disable(request.PathValue("id"))
	}
	if err != nil {
		handleBusinessError(response, err)
		return
	}
	writeJSON(response, http.StatusOK, updated)
}

func (a *api) updateSettings(response http.ResponseWriter, request *http.Request) {
	var input settingsRequest
	if err := decodeJSON(response, request, &input); err != nil {
		return
	}
	if input.AutoSwitch == nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "auto_switch is required", "auto_switch")
		return
	}
	if input.UnmatchedAction == nil {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "unmatched_action is required", "unmatched_action")
		return
	}

	language := a.rules.Snapshot().General.Language
	if input.Language != nil {
		language = *input.Language
	}
	updated, err := a.rules.UpdateGeneral(config2.GeneralSettings{
		AutoSwitch:      *input.AutoSwitch,
		UnmatchedAction: *input.UnmatchedAction,
		Language:        language,
	})
	if err != nil {
		handleBusinessError(response, err)
		return
	}
	if a.onSettingsUpdated != nil {
		a.onSettingsUpdated(updated)
	}
	writeJSON(response, http.StatusOK, updated)
}

func decodeJSON(response http.ResponseWriter, request *http.Request, destination any) error {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeAPIError(response, http.StatusUnsupportedMediaType, "invalid_content_type", "Content-Type must be application/json", "")
		return errors.New("invalid content type")
	}

	request.Body = http.MaxBytesReader(response, request.Body, maxJSONBodySize)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAPIError(response, http.StatusRequestEntityTooLarge, "body_too_large", fmt.Sprintf("request body cannot exceed %d bytes", maxJSONBodySize), "")
			return err
		}
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "invalid JSON request body: "+err.Error(), "")
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeAPIError(response, http.StatusBadRequest, "invalid_json", "JSON request body must contain exactly one object", "")
		return errors.New("trailing JSON value")
	}
	return nil
}

func handleBusinessError(response http.ResponseWriter, err error) {
	if errors.Is(err, rule.ErrNotFound) {
		writeAPIError(response, http.StatusNotFound, "rule_not_found", "Rule not found", "")
		return
	}
	var validationErr *config2.ValidationError
	if errors.As(err, &validationErr) {
		writeAPIError(response, http.StatusUnprocessableEntity, "validation_failed", validationErr.Message, validationErr.Field)
		return
	}
	log.Printf("API operation failed: %v", err)
	writeAPIError(response, http.StatusInternalServerError, "internal_error", "Operation failed; check the application log", "")
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		log.Printf("Could not write JSON response: %v", err)
	}
}

func writeAPIError(response http.ResponseWriter, status int, code, message, field string) {
	writeJSON(response, status, apiErrorEnvelope{Error: apiError{Code: code, Message: message, Field: field}})
}

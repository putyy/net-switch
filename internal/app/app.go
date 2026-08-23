package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/putyy/net-switch/internal/applog"
	"github.com/putyy/net-switch/internal/autostart"
	"github.com/putyy/net-switch/internal/browser"
	config2 "github.com/putyy/net-switch/internal/config"
	"github.com/putyy/net-switch/internal/instance"
	network2 "github.com/putyy/net-switch/internal/network"
	"github.com/putyy/net-switch/internal/platform"
	"github.com/putyy/net-switch/internal/rule"
	server2 "github.com/putyy/net-switch/internal/server"
	"github.com/putyy/net-switch/internal/tray"
	webui "github.com/putyy/net-switch/web"
)

const (
	shutdownTimeout         = 3 * time.Second
	networkReadTimeout      = 5 * time.Second
	networkEventDebounce    = 500 * time.Millisecond
	networkResyncInterval   = time.Minute
	networkOperationTimeout = 2 * time.Minute
)

type Options struct {
	Version    string
	DryRun     bool
	LoginStart bool
	Logs       *applog.Manager
}

type App struct {
	options     Options
	ruleManager *rule.Manager
}

func New(options Options) *App {
	return &App{options: options}
}

func (a *App) Run(ctx context.Context) error {
	runningInstance, err := instance.Acquire(!a.options.LoginStart)
	if errors.Is(err, instance.ErrAlreadyRunning) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("initialize single-instance runtime: %w", err)
	}
	defer func() {
		if closeErr := runningInstance.Close(); closeErr != nil {
			log.Printf("Could not close the single-instance runtime: %v", closeErr)
		}
	}()

	configStore, err := config2.NewStore()
	if err != nil {
		return fmt.Errorf("initialize configuration storage: %w", err)
	}
	configuration, err := configStore.LoadOrCreate()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	a.ruleManager, err = rule.NewManager(configStore, configuration)
	if err != nil {
		return fmt.Errorf("initialize rule manager: %w", err)
	}
	log.Printf("Loaded configuration from %s with %d rule(s)", configStore.Path(), len(a.ruleManager.List()))

	networkReader := platform.NewReader()
	var networkWatcher network2.ChangeWatcher
	platformWatcher, watcherErr := platform.NewWatcher()
	if watcherErr != nil {
		log.Printf("Could not watch network changes; periodic synchronization will be used: %v", watcherErr)
	} else {
		networkWatcher = platformWatcher
	}
	if networkWatcher != nil {
		defer func() {
			if closeErr := networkWatcher.Close(); closeErr != nil {
				log.Printf("Could not close the network watcher: %v", closeErr)
			}
		}()
	}
	var requestPlatformPermissions func()
	if requester, ok := networkWatcher.(interface{ RequestPermissions() }); ok {
		requestPlatformPermissions = requester.RequestPermissions
	}
	networkMonitor, err := network2.NewMonitor(networkReader, networkWatcher, network2.MonitorOptions{
		DebounceDelay:  networkEventDebounce,
		ResyncInterval: networkResyncInterval,
		ReadTimeout:    networkReadTimeout,
	})
	if err != nil {
		return fmt.Errorf("initialize network monitor: %w", err)
	}
	networkOperator := platform.NewOperator(a.options.DryRun)
	autoSwitcher, err := network2.NewAutoSwitcher(a.ruleManager, networkOperator)
	if err != nil {
		return fmt.Errorf("initialize automatic switch coordinator: %w", err)
	}
	autoStartManager, autoStartInitErr := autostart.New(a.options.LoginStart)

	runtimeCtx, stopRuntime := context.WithCancel(ctx)
	defer stopRuntime()
	trayStatusUpdates := make(chan tray.Status, 1)
	autoStartUpdates := make(chan bool, 1)
	autoSwitchRequests := make(chan network2.State, 1)
	publishTrayStatus(trayStatusUpdates, tray.Status{
		Network:    networkMonitor.Snapshot(),
		AutoSwitch: configuration.General.AutoSwitch,
		Language:   configuration.General.Language,
	})
	go func() {
		for {
			select {
			case <-runtimeCtx.Done():
				return
			case update := <-networkMonitor.Updates():
				if update.Err != nil {
					log.Printf("Could not refresh network status: %v", update.Err)
				}
				general := a.ruleManager.Snapshot().General
				publishTrayStatus(trayStatusUpdates, tray.Status{
					Network:    update.State,
					AutoSwitch: general.AutoSwitch,
					Language:   general.Language,
				})
				publishNetworkState(autoSwitchRequests, update.State)
			}
		}
	}()
	go networkMonitor.Run(runtimeCtx)

	var operationMu sync.RWMutex
	var autoSwitchStatusMu sync.RWMutex
	var networkOperationMu sync.Mutex
	var autoOperationCancelMu sync.Mutex
	var autoStartMu sync.Mutex
	var lastOperation *network2.OperationResult
	var lastAutoSwitch *network2.AutoSwitchStatus
	var cancelAutoOperation context.CancelFunc
	cancelAutomaticOperation := func() {
		autoOperationCancelMu.Lock()
		if cancelAutoOperation != nil {
			cancelAutoOperation()
		}
		autoOperationCancelMu.Unlock()
	}
	rememberOperation := func(result network2.OperationResult) {
		operationMu.Lock()
		stored := result
		lastOperation = &stored
		operationMu.Unlock()
	}
	loadLastOperation := func() *network2.OperationResult {
		operationMu.RLock()
		defer operationMu.RUnlock()
		if lastOperation == nil {
			return nil
		}
		stored := *lastOperation
		return &stored
	}
	rememberAutoSwitch := func(status network2.AutoSwitchStatus) {
		autoSwitchStatusMu.Lock()
		stored := status
		lastAutoSwitch = &stored
		autoSwitchStatusMu.Unlock()
	}
	loadLastAutoSwitch := func() *network2.AutoSwitchStatus {
		autoSwitchStatusMu.RLock()
		defer autoSwitchStatusMu.RUnlock()
		if lastAutoSwitch == nil {
			return nil
		}
		stored := *lastAutoSwitch
		return &stored
	}
	refreshAfterOperation := func() {
		refreshCtx, cancel := context.WithTimeout(runtimeCtx, networkReadTimeout)
		defer cancel()
		update := networkMonitor.Refresh(refreshCtx)
		if update.Err != nil {
			log.Printf("Could not refresh network status after an operation: %v", update.Err)
		}
	}
	applyMatchedRule := func(ctx context.Context) (network2.OperationResult, error) {
		networkOperationMu.Lock()
		defer networkOperationMu.Unlock()

		current := networkMonitor.Snapshot()
		if current.Status != network2.StateStatusConnected || current.SSID == "" || current.Service == "" || current.Interface == "" {
			result := failedOperation(network2.OperationApplyRule, "operation.network_unavailable", "The current Wi-Fi state cannot be used to apply a rule")
			rememberOperation(result)
			return result, fmt.Errorf("%w: %s", network2.ErrNetworkUnavailable, result.Message)
		}
		matched, ok := a.ruleManager.MatchSSID(current.SSID)
		if !ok {
			result := failedOperation(network2.OperationApplyRule, "operation.no_matched_rule", "No enabled rule matches the current Wi-Fi")
			rememberOperation(result)
			return result, fmt.Errorf("%w: %s", network2.ErrNoMatchedRule, result.Message)
		}

		operationCtx, cancel := context.WithTimeout(ctx, networkOperationTimeout)
		defer cancel()
		result, operationErr := networkOperator.Apply(operationCtx, current, matched.IPv4)
		result.Trigger = network2.OperationTriggerManual
		result.RuleID = matched.ID
		result.RuleName = matched.Name
		rememberOperation(result)
		if result.Plan != nil && !result.DryRun {
			refreshAfterOperation()
		}
		logOperationResult(result, operationErr)
		return result, operationErr
	}
	restoreDHCP := func(ctx context.Context) (network2.OperationResult, error) {
		networkOperationMu.Lock()
		defer networkOperationMu.Unlock()

		current := networkMonitor.Snapshot()
		if current.Service == "" || current.Interface == "" {
			result := failedOperation(network2.OperationRestoreDHCP, "operation.no_dhcp_service", "No Wi-Fi service is available for DHCP restoration")
			rememberOperation(result)
			return result, fmt.Errorf("%w: %s", network2.ErrNetworkUnavailable, result.Message)
		}

		operationCtx, cancel := context.WithTimeout(ctx, networkOperationTimeout)
		defer cancel()
		result, operationErr := networkOperator.RestoreDHCP(operationCtx, current)
		result.Trigger = network2.OperationTriggerManual
		rememberOperation(result)
		if result.Success && !result.DryRun {
			autoSwitcher.SuppressCurrentNetwork(current)
		}
		if result.Plan != nil && !result.DryRun {
			refreshAfterOperation()
		}
		logOperationResult(result, operationErr)
		return result, operationErr
	}
	go func() {
		for {
			select {
			case <-runtimeCtx.Done():
				return
			case <-autoSwitchRequests:
				networkOperationMu.Lock()
				operationCtx, cancel := context.WithTimeout(runtimeCtx, networkOperationTimeout)
				autoOperationCancelMu.Lock()
				cancelAutoOperation = cancel
				autoOperationCancelMu.Unlock()
				outcome, operationErr := autoSwitcher.Reconcile(operationCtx, networkMonitor.Snapshot())
				autoOperationCancelMu.Lock()
				cancelAutoOperation = nil
				autoOperationCancelMu.Unlock()
				cancel()
				rememberAutoSwitch(outcome.Status)
				if outcome.Result != nil {
					rememberOperation(*outcome.Result)
					if outcome.Result.Plan != nil && !outcome.Result.DryRun {
						refreshAfterOperation()
					}
					logOperationResult(*outcome.Result, operationErr)
				} else {
					log.Printf("Automatic switch decision (%s): %s", outcome.Status.Decision, outcome.Status.Message)
				}
				networkOperationMu.Unlock()
			}
		}
	}()
	var autoStartState func(context.Context) (bool, error)
	var setAutoStart func(context.Context, bool) (bool, error)
	if autoStartManager != nil {
		autoStartState = func(ctx context.Context) (bool, error) {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			default:
			}
			autoStartMu.Lock()
			defer autoStartMu.Unlock()
			return autoStartManager.Enabled()
		}
		setAutoStart = func(ctx context.Context, enabled bool) (bool, error) {
			select {
			case <-ctx.Done():
				return false, ctx.Err()
			default:
			}
			autoStartMu.Lock()
			defer autoStartMu.Unlock()
			if setErr := autoStartManager.SetEnabled(enabled); setErr != nil {
				return false, setErr
			}
			actual, stateErr := autoStartManager.Enabled()
			if stateErr != nil {
				return false, stateErr
			}
			publishAutoStartState(autoStartUpdates, actual)
			log.Printf("Start at login set to %t", actual)
			return actual, nil
		}
	}
	autoStartEnabled := false
	autoStartStateErr := autoStartInitErr
	if autoStartStateErr == nil && autoStartState != nil {
		autoStartEnabled, autoStartStateErr = autoStartState(runtimeCtx)
	}
	if autoStartStateErr != nil {
		log.Printf("Could not read start-at-login status: %v", autoStartStateErr)
	}
	var recentLogs func(context.Context, int) ([]string, error)
	if a.options.Logs != nil {
		recentLogs = a.options.Logs.Recent
	}

	localServer, err := server2.Start(webui.Files, server2.Dependencies{
		Version: a.options.Version,
		Rules:   a.ruleManager,
		CurrentState: func(context.Context) network2.RuntimeState {
			currentNetwork := networkMonitor.Snapshot()
			runtimeState := network2.RuntimeState{Network: currentNetwork}
			if currentNetwork.SSID != "" {
				if matched, ok := a.ruleManager.MatchSSID(currentNetwork.SSID); ok {
					runtimeState.MatchedRuleID = matched.ID
					comparison := network2.CompareIPv4(currentNetwork, matched.IPv4)
					runtimeState.TargetComparison = &comparison
				}
			}
			runtimeState.LastOperation = loadLastOperation()
			runtimeState.LastAutoSwitch = loadLastAutoSwitch()
			return runtimeState
		},
		OnSettingsUpdated: func(settings config2.GeneralSettings) {
			autoSwitcher.Reset()
			if !settings.AutoSwitch {
				cancelAutomaticOperation()
			}
			publishTrayStatus(trayStatusUpdates, tray.Status{
				Network:    networkMonitor.Snapshot(),
				AutoSwitch: settings.AutoSwitch,
				Language:   settings.Language,
			})
			if settings.AutoSwitch {
				publishNetworkState(autoSwitchRequests, networkMonitor.Snapshot())
			}
		},
		ApplyMatchedRule: applyMatchedRule,
		RestoreDHCP:      restoreDHCP,
		AutoStartState:   autoStartState,
		SetAutoStart:     setAutoStart,
		RecentLogs:       recentLogs,
	})
	if err != nil {
		return fmt.Errorf("start local management service: %w", err)
	}

	log.Printf("Net Switch %s started; dashboard: %s", a.options.Version, localServer.URL())
	if a.options.DryRun {
		log.Print("Dry-run mode is enabled; network operations will not change system settings")
	}
	if a.options.LoginStart {
		log.Print("Started by a login item")
	}

	go func() {
		for {
			select {
			case <-runtimeCtx.Done():
				return
			case <-runningInstance.OpenRequests():
				if openErr := browser.Open(localServer.DashboardURL()); openErr != nil {
					log.Printf("Could not open the dashboard for a repeated launch request: %v", openErr)
				}
			}
		}
	}()

	serverErr := make(chan error, 1)
	go func() {
		if serveErr := <-localServer.Done(); serveErr != nil {
			serverErr <- serveErr
			tray.Quit()
		}
	}()

	tray.Run(runtimeCtx, tray.Actions{
		RequestPermissions: requestPlatformPermissions,
		OpenDashboard: func() {
			if openErr := browser.Open(localServer.DashboardURL()); openErr != nil {
				log.Printf("Could not open the dashboard: %v", openErr)
			}
		},
		AutoStartAvailable: autoStartStateErr == nil,
		AutoStartEnabled:   autoStartEnabled,
		AutoStartUpdates:   autoStartUpdates,
		InitialStatus: tray.Status{
			Network:    networkMonitor.Snapshot(),
			AutoSwitch: a.ruleManager.Snapshot().General.AutoSwitch,
			Language:   a.ruleManager.Snapshot().General.Language,
		},
		StatusUpdates: trayStatusUpdates,
		SetAutoStart: func(enabled bool) error {
			if setAutoStart == nil {
				return errors.New("start-at-login manager is unavailable")
			}
			if _, setErr := setAutoStart(runtimeCtx, enabled); setErr != nil {
				log.Printf("Could not update start-at-login status: %v", setErr)
				return setErr
			}
			return nil
		},
		ApplyMatchedRule: func() error {
			_, operationErr := applyMatchedRule(runtimeCtx)
			if operationErr != nil {
				log.Printf("Tray rule application failed: %v", operationErr)
			}
			return operationErr
		},
		RestoreDHCP: func() error {
			_, operationErr := restoreDHCP(runtimeCtx)
			if operationErr != nil {
				log.Printf("Tray DHCP restoration failed: %v", operationErr)
			}
			return operationErr
		},
		ToggleAutoSwitch: func() error {
			configuration := a.ruleManager.Snapshot()
			configuration.General.AutoSwitch = !configuration.General.AutoSwitch
			updated, updateErr := a.ruleManager.UpdateGeneral(configuration.General)
			if updateErr != nil {
				log.Printf("Could not toggle automatic switching: %v", updateErr)
				return updateErr
			}
			autoSwitcher.Reset()
			if !updated.AutoSwitch {
				cancelAutomaticOperation()
			}
			publishTrayStatus(trayStatusUpdates, tray.Status{
				Network:    networkMonitor.Snapshot(),
				AutoSwitch: updated.AutoSwitch,
				Language:   updated.Language,
			})
			if updated.AutoSwitch {
				publishNetworkState(autoSwitchRequests, networkMonitor.Snapshot())
			}
			return nil
		},
	})
	stopRuntime()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := localServer.Close(shutdownCtx)

	select {
	case serveErr := <-serverErr:
		return fmt.Errorf("local management service stopped unexpectedly: %w", serveErr)
	default:
	}

	if shutdownErr != nil && !errors.Is(shutdownErr, context.Canceled) {
		return fmt.Errorf("close local management service: %w", shutdownErr)
	}
	return nil
}

func failedOperation(action network2.OperationAction, messageKey, message string) network2.OperationResult {
	return network2.OperationResult{
		Action:      action,
		Trigger:     network2.OperationTriggerManual,
		Message:     message,
		MessageKey:  messageKey,
		CompletedAt: time.Now(),
	}
}

func logOperationResult(result network2.OperationResult, err error) {
	if err != nil {
		log.Printf("Network operation failed (%s): %v", result.Action, err)
		return
	}
	log.Printf("Network operation completed (%s): %s", result.Action, result.Message)
}

func publishTrayStatus(updates chan tray.Status, status tray.Status) {
	select {
	case updates <- status:
		return
	default:
	}
	select {
	case <-updates:
	default:
	}
	select {
	case updates <- status:
	default:
	}
}

func publishAutoStartState(updates chan bool, enabled bool) {
	select {
	case updates <- enabled:
		return
	default:
	}
	select {
	case <-updates:
	default:
	}
	select {
	case updates <- enabled:
	default:
	}
}

func publishNetworkState(updates chan network2.State, state network2.State) {
	select {
	case updates <- state:
		return
	default:
	}
	select {
	case <-updates:
	default:
	}
	select {
	case updates <- state:
	default:
	}
}

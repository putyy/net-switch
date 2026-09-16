package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/putyy/net-switch/internal/config"
	"github.com/putyy/net-switch/internal/network"
)

func readyLoginNetwork() network.State {
	return network.State{Status: network.StateStatusConnected, SSID: "Home", Service: "Wi-Fi", Interface: "en0", IPv4Address: "192.168.1.2", Gateway: "192.168.1.1", Mode: network.AddressModeDHCP, DNSMode: network.DNSModeAutomatic}
}

func TestLoginExitEligibility(t *testing.T) {
	for _, tt := range []struct {
		name string
		edit func(*Options, *config.GeneralSettings, *network.State, *network.AutoSwitchOutcome)
		want bool
	}{
		{name: "completed", want: true},
		{name: "manual launch", edit: func(o *Options, _ *config.GeneralSettings, _ *network.State, _ *network.AutoSwitchOutcome) {
			o.LoginStart = false
		}},
		{name: "dry run", edit: func(o *Options, _ *config.GeneralSettings, _ *network.State, _ *network.AutoSwitchOutcome) {
			o.DryRun = true
		}},
		{name: "setting disabled", edit: func(_ *Options, g *config.GeneralSettings, _ *network.State, _ *network.AutoSwitchOutcome) {
			g.ExitAfterLogin = false
		}},
		{name: "automatic switching paused", edit: func(_ *Options, g *config.GeneralSettings, _ *network.State, _ *network.AutoSwitchOutcome) {
			g.AutoSwitch = false
		}},
		{name: "disconnected", edit: func(_ *Options, _ *config.GeneralSettings, s *network.State, _ *network.AutoSwitchOutcome) {
			s.Status = network.StateStatusDisconnected
		}},
		{name: "DHCP lease pending", edit: func(_ *Options, _ *config.GeneralSettings, s *network.State, _ *network.AutoSwitchOutcome) {
			s.IPv4Address = ""
		}},
		{name: "self assigned address", edit: func(_ *Options, _ *config.GeneralSettings, s *network.State, _ *network.AutoSwitchOutcome) {
			s.IPv4Address = "169.254.1.2"
		}},
		{name: "no gateway", edit: func(_ *Options, _ *config.GeneralSettings, s *network.State, _ *network.AutoSwitchOutcome) {
			s.Gateway = ""
		}},
		{name: "network changed", edit: func(_ *Options, _ *config.GeneralSettings, s *network.State, _ *network.AutoSwitchOutcome) {
			s.SSID = "Office"
		}},
		{name: "failed operation", edit: func(_ *Options, _ *config.GeneralSettings, _ *network.State, r *network.AutoSwitchOutcome) {
			r.Status.Success = false
		}},
		{name: "just restored DHCP", edit: func(_ *Options, _ *config.GeneralSettings, _ *network.State, r *network.AutoSwitchOutcome) {
			r.Status.Decision = network.AutoSwitchRestored
		}},
		{name: "kept configuration", want: true, edit: func(_ *Options, _ *config.GeneralSettings, _ *network.State, r *network.AutoSwitchOutcome) {
			r.Status.Decision = network.AutoSwitchKept
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			options := Options{LoginStart: true}
			general := config.Default().General
			general.ExitAfterLogin = true
			current := readyLoginNetwork()
			outcome := network.AutoSwitchOutcome{Status: network.AutoSwitchStatus{SSID: current.SSID, Success: true, Decision: network.AutoSwitchMatched}}
			if tt.edit != nil {
				tt.edit(&options, &general, &current, &outcome)
			}
			if got := loginExitEligible(options, general, current, outcome); got != tt.want {
				t.Fatalf("eligible = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestLoginExitRequiresConsecutiveChecksOnSameConfiguration(t *testing.T) {
	var progress loginExitProgress
	configuration := config.Default()
	current := readyLoginNetwork()
	if progress.observe(configuration, current, true) {
		t.Fatal("exited after only one check")
	}
	if progress.observe(configuration, current, false) || progress.observe(configuration, current, true) {
		t.Fatal("a failed check did not reset progress")
	}
	current.SSID = "Office"
	if progress.observe(configuration, current, true) {
		t.Fatal("a Wi-Fi change did not reset progress")
	}
	current.IPv4Address = "192.168.1.3"
	if progress.observe(configuration, current, true) {
		t.Fatal("an IP change did not reset progress")
	}
	configuration.General.UnmatchedAction = config.UnmatchedKeep
	if progress.observe(configuration, current, true) {
		t.Fatal("a settings change did not reset progress")
	}
	if !progress.observe(configuration, current, true) {
		t.Fatal("did not exit after two consecutive successful checks")
	}
}

func TestInternetProbe(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodHead || r.URL.RawQuery != "" {
			t.Errorf("unexpected connectivity request: %s %s", r.Method, r.URL)
		}
		switch r.URL.Path {
		case "/online":
			w.WriteHeader(http.StatusOK)
		case "/portal":
			w.Header().Set("Location", "/online")
			w.WriteHeader(http.StatusFound)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if probeInternet(context.Background(), client, []string{server.URL + "/portal"}) {
		t.Fatal("captive portal redirect counted as Internet access")
	}
	if probeInternet(context.Background(), client, []string{server.URL + "/failure"}) {
		t.Fatal("failed response counted as Internet access")
	}
	if !probeInternet(context.Background(), client, []string{server.URL + "/failure", server.URL + "/online"}) {
		t.Fatal("fallback endpoint was not used")
	}
	before := requests.Load()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if probeInternet(ctx, client, []string{server.URL + "/online"}) || requests.Load() != before {
		t.Fatal("cancelled probe issued a request or succeeded")
	}
}

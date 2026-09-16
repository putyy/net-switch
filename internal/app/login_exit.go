package app

import (
	"context"
	"net/http"
	"net/netip"
	"reflect"
	"time"

	"github.com/putyy/net-switch/internal/config"
	"github.com/putyy/net-switch/internal/network"
)

const loginExitInterval = 5 * time.Second

// Only completed, non-mutating decisions are eligible. After applying settings,
// wait for the next reconciliation and a usable address (DHCP may still be pending).
func loginExitEligible(options Options, general config.GeneralSettings, current network.State, outcome network.AutoSwitchOutcome) bool {
	if !options.LoginStart || options.DryRun || !general.ExitAfterLogin || !general.AutoSwitch {
		return false
	}
	if current.Status != network.StateStatusConnected || current.SSID == "" || current.Service == "" || current.Interface == "" || outcome.Status.SSID != current.SSID || !outcome.Status.Success {
		return false
	}
	if !usableIPv4(current.IPv4Address) || !usableIPv4(current.Gateway) {
		return false
	}
	switch outcome.Status.Decision {
	case network.AutoSwitchMatched, network.AutoSwitchKept, network.AutoSwitchSuppressed:
		return true
	default:
		return false
	}
}

func usableIPv4(value string) bool {
	address, err := netip.ParseAddr(value)
	return err == nil && address.Is4() && address.IsGlobalUnicast() && !address.IsLinkLocalUnicast()
}

type loginExitProgress struct {
	state         network.State
	configuration config.Config
	successes     int
}

func (p *loginExitProgress) observe(configuration config.Config, current network.State, online bool) bool {
	if !online {
		*p = loginExitProgress{}
		return false
	}
	if !reflect.DeepEqual(p.state, current) || !reflect.DeepEqual(p.configuration, configuration) {
		p.successes = 0
	}
	p.state = current
	p.configuration = configuration
	p.successes++
	return p.successes >= 2
}

// Requests contain no SSID, IP configuration, or rules. Use independent HTTPS
// sites and reject redirects so a captive-portal login is not counted as online.
func newInternetProbe() func(context.Context) bool {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DisableKeepAlives = true
	client := &http.Client{
		Transport: transport,
		Timeout:   2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return func(ctx context.Context) bool {
		return probeInternet(ctx, client, []string{"https://www.baidu.com/", "https://www.apple.com/"})
	}
}

func probeInternet(ctx context.Context, client *http.Client, urls []string) bool {
	for _, url := range urls {
		if ctx.Err() != nil {
			return false
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
		if err != nil {
			continue
		}
		response, err := client.Do(request)
		if err != nil {
			continue
		}
		response.Body.Close()
		if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
			return true
		}
	}
	return false
}

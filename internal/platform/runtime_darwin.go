//go:build darwin

package platform

import (
	network2 "github.com/putyy/net-switch/internal/network"
	"github.com/putyy/net-switch/internal/platform/darwin"
)

func NewReader() network2.Reader {
	return darwin.NewReader()
}

func NewWatcher() (network2.ChangeWatcher, error) {
	return darwin.NewWatcher()
}

func NewOperator(dryRun bool) network2.AutoSwitchOperator {
	return darwin.NewOperator(dryRun)
}

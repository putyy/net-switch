//go:build windows

package platform

import (
	network2 "github.com/putyy/net-switch/internal/network"
	platformwindows "github.com/putyy/net-switch/internal/platform/windows"
)

func NewReader() network2.Reader {
	return platformwindows.NewReader()
}

func NewWatcher() (network2.ChangeWatcher, error) {
	return platformwindows.NewWatcher()
}

func NewOperator(dryRun bool) network2.AutoSwitchOperator {
	return platformwindows.NewOperator(dryRun)
}

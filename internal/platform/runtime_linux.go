//go:build linux

package platform

import (
	network2 "github.com/putyy/net-switch/internal/network"
	platformlinux "github.com/putyy/net-switch/internal/platform/linux"
)

func NewReader() network2.Reader {
	return platformlinux.NewReader()
}

func NewWatcher() (network2.ChangeWatcher, error) {
	return platformlinux.NewWatcher()
}

func NewOperator(dryRun bool) network2.AutoSwitchOperator {
	return platformlinux.NewOperator(dryRun)
}

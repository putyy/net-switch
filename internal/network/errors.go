package network

import "errors"

var (
	ErrNoMatchedRule      = errors.New("no enabled rule matches the current network")
	ErrNetworkUnavailable = errors.New("the current Wi-Fi state cannot be used for this operation")
)

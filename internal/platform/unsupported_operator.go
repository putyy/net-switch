package platform

import (
	"context"
	"errors"
	"runtime"
	"time"

	"github.com/putyy/net-switch/internal/config"
	"github.com/putyy/net-switch/internal/network"
)

type unsupportedOperator struct {
	dryRun bool
}

func (o *unsupportedOperator) Apply(_ context.Context, _ network.State, _ config.IPv4Config) (network.OperationResult, error) {
	return o.result(network.OperationApplyRule)
}

func (o *unsupportedOperator) RestoreDHCP(_ context.Context, _ network.State) (network.OperationResult, error) {
	return o.result(network.OperationRestoreDHCP)
}

func (o *unsupportedOperator) result(action network.OperationAction) (network.OperationResult, error) {
	err := errors.New("this version does not support changing network settings on " + runtime.GOOS)
	return network.OperationResult{
		Action:      action,
		DryRun:      o.dryRun,
		Message:     err.Error(),
		MessageKey:  "operation.unsupported",
		CompletedAt: time.Now(),
	}, err
}

package app

import (
	"context"
	"log/slog"
	"sync/atomic"
)

type NoopComponent struct {
	name    string
	logger  *slog.Logger
	started atomic.Bool
}

func (n *NoopComponent) Start(_ context.Context) error {
	if n.logger == nil {
		n.logger = slog.Default()
	}
	n.started.Store(true)
	n.logger.Info("component start (noop)", "component", n.name)
	return nil
}

func (n *NoopComponent) Stop(_ context.Context) error {
	if n.logger == nil {
		n.logger = slog.Default()
	}
	if n.started.Load() {
		n.logger.Info("component stop (noop)", "component", n.name)
	}
	n.started.Store(false)
	return nil
}

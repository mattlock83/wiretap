package core

import (
	"context"
	"errors"
	"log/slog"
)

type Component interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

type Lifecycle struct {
	Proxy  Component
	Logger *slog.Logger
}

func (l *Lifecycle) Start(ctx context.Context) error {
	if l == nil {
		return errors.New("lifecycle is nil")
	}
	logger := l.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if l.Proxy != nil {
		logger.Info("starting proxy")
		if err := l.Proxy.Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (l *Lifecycle) Stop(ctx context.Context) error {
	if l == nil {
		return errors.New("lifecycle is nil")
	}
	logger := l.Logger
	if logger == nil {
		logger = slog.Default()
	}
	var errs []error
	if l.Proxy != nil {
		logger.Info("stopping proxy")
		if err := l.Proxy.Stop(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

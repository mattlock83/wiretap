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
	Mock   Component
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
	startedProxy := false
	if l.Proxy != nil {
		logger.Info("starting proxy")
		if err := l.Proxy.Start(ctx); err != nil {
			return err
		}
		startedProxy = true
	}
	if l.Mock != nil {
		logger.Info("starting mock server")
		if err := l.Mock.Start(ctx); err != nil {
			if startedProxy {
				logger.Info("stopping proxy after mock start failure")
				_ = l.Proxy.Stop(ctx)
			}
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
	if l.Mock != nil {
		logger.Info("stopping mock server")
		if err := l.Mock.Stop(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	if l.Proxy != nil {
		logger.Info("stopping proxy")
		if err := l.Proxy.Stop(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

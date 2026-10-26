package app

import (
	"context"
	"errors"
	"time"

	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/proxy"
	"github.com/mattlock83/wiretap/internal/runoutput"
)

func (a *App) startRun(ctx context.Context) error {
	if err := a.startProxy(ctx); err != nil && !errors.Is(err, proxy.ErrAlreadyStarted) {
		return err
	}
	_, err := a.ensureBundle()
	if err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return errors.Join(err, a.proxyServer.Stop(stopCtx))
	}
	return nil
}

func (a *App) finishRun(ctx context.Context) (string, *runoutput.Snapshot, error) {
	a.sessionsMu.Lock()
	activeSessions := len(a.sessions) != 0
	a.sessionsMu.Unlock()
	if activeSessions {
		return "", nil, errors.New("capture sessions are still active; teardown tests before finishing the run")
	}
	if a.hasActiveSessions() {
		return "", nil, errors.New("capture exchanges are still active")
	}
	if err := a.proxyServer.Stop(ctx); err != nil {
		return "", nil, err
	}
	current := a.currentBundle()
	if err := a.closeBundle("complete"); err != nil {
		return "", nil, err
	}
	a.resetCaptureRouting()
	if current == nil {
		return "", nil, nil
	}
	snapshot, err := runoutput.Load(ctx, current.Path(), "", false)
	return current.Path(), snapshot, err
}
func (a *App) interruptRun() error {
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stopErr := a.proxyServer.Stop(ctx)
	current := a.currentBundle()
	err := a.closeBundle("interrupted")
	if err == nil && current != nil {
		_, _, err = bundle.Load(current.Path())
	}
	return errors.Join(stopErr, err)
}

func (a *App) startProxy(ctx context.Context) error {
	a.logger.Info("starting proxy")
	info, err := a.proxyServer.Start(ctx)
	if err == nil {
		a.proxyInfo = info
	}
	return err
}

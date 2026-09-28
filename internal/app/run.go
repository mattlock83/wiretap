package app

import (
	"context"
	"errors"
	"time"

	"github.com/phrond/evermock/internal/bundlereport"
	"github.com/phrond/evermock/internal/proxy"
)

func (a *App) startRun(ctx context.Context) error {
	if err := a.lifecycle.Start(ctx); err != nil && !errors.Is(err, proxy.ErrAlreadyStarted) {
		return err
	}
	_, err := a.ensureBundle()
	if err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return errors.Join(err, a.lifecycle.Stop(stopCtx))
	}
	return nil
}

func (a *App) finishRun(ctx context.Context) (string, error) {
	if a.hasActiveSessions() {
		return "", errors.New("capture sessions are still active; teardown tests before finishing the run")
	}
	if err := a.lifecycle.Stop(ctx); err != nil {
		return "", err
	}
	current := a.currentBundle()
	if err := a.closeBundle("complete"); err != nil {
		return "", err
	}
	a.resetCaptureRouting()
	if current == nil {
		return "", nil
	}
	_, err := bundlereport.Refresh(current.Path())
	return current.Path(), err
}
func (a *App) interruptRun() error {
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stopErr := a.lifecycle.Stop(ctx)
	current := a.currentBundle()
	err := a.closeBundle("interrupted")
	if err == nil && current != nil {
		_, err = bundlereport.Refresh(current.Path())
	}
	return errors.Join(stopErr, err)
}

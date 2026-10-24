package app

import (
	"context"
	"errors"
	"time"

	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/proxy"
)

func (a *App) startRun(ctx context.Context) error {
	if a.proxyComponent != nil {
		a.proxyComponent.server.SetGRPCProvider(a.project != nil && a.project.SUT.Protocol == "grpc")
	}
	if err := a.lifecycle.Start(ctx); err != nil && !errors.Is(err, proxy.ErrAlreadyStarted) {
		return err
	}
	if err := a.startGRPCListeners(); err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = a.lifecycle.Stop(stopCtx)
		return err
	}
	_, err := a.ensureBundle()
	if err != nil {
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return errors.Join(err, a.stopGRPCListeners(stopCtx), a.lifecycle.Stop(stopCtx))
	}
	return nil
}

func (a *App) finishRun(ctx context.Context) (string, error) {
	a.sessionsMu.Lock()
	activeSessions := len(a.sessions) != 0
	a.sessionsMu.Unlock()
	if activeSessions {
		return "", errors.New("capture sessions are still active; teardown tests before finishing the run")
	}
	drainCtx, cancelDrain := context.WithTimeout(ctx, 5*time.Second)
	defer cancelDrain()
	if err := a.stopGRPCCalls(drainCtx); err != nil {
		return "", err
	}
	if a.hasActiveSessions() {
		return "", errors.New("capture exchanges are still active")
	}
	refs, _, err := a.resolveGRPCContracts(ctx)
	if err != nil {
		return "", err
	}
	if current := a.currentBundle(); current != nil && len(current.Snapshot().Schemas) == 0 && len(refs) > 0 {
		if a.project != nil {
			kinds := map[string]string{}
			for _, id := range a.project.DependencyIDs() {
				kinds[id] = a.project.Dependencies[id].Kind
			}
			id := a.project.SUT.ID
			if id == "" {
				id = "sut"
			}
			if err := current.Configure(bundle.Identity{APIID: id, SUTTargetKind: a.project.SUT.Kind, DependencyKinds: kinds}); err != nil {
				return "", err
			}
		}
		if err := current.RegisterSchemas(refs); err != nil {
			return "", err
		}
	}
	if err := a.stopGRPCListeners(ctx); err != nil {
		return "", err
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
	_, _, err = bundle.Load(current.Path())
	return current.Path(), err
}
func (a *App) interruptRun() error {
	a.controlMu.Lock()
	defer a.controlMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stopErr := a.lifecycle.Stop(ctx)
	stopErr = errors.Join(stopErr, a.stopGRPCListeners(ctx))
	current := a.currentBundle()
	err := a.closeBundle("interrupted")
	if err == nil && current != nil {
		_, _, err = bundle.Load(current.Path())
	}
	return errors.Join(stopErr, err)
}

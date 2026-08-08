package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/phrond/evermock/internal/proxy"
)

// runtimeSession owns the capture state for one consumer test. Phrond does
// not own the SUT lifecycle; it only records traffic routed through its proxy.
type runtimeSession struct {
	ID         string
	Name       string
	Store      *TestCaseStore
	SUTBaseURL string
	Explicit   bool
	CreatedAt  time.Time
}

func (a *App) nextSessionID() string {
	a.sessionSeq++
	return fmt.Sprintf("session-%d-%d", time.Now().UTC().UnixNano(), a.sessionSeq)
}

func (a *App) recordProxyForSession(exchange proxy.HTTPExchange) {
	sessionID := strings.TrimSpace(exchange.SessionID)

	a.sessionsMu.Lock()
	if sessionID == "" {
		sessionID = a.defaultSessionID
	}
	session := a.sessions[sessionID]
	a.sessionsMu.Unlock()

	if session != nil && session.Store != nil {
		session.Store.RecordHTTP(exchange)
	}
}

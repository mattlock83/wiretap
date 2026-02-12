package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/phrond/evermock/internal/mock"
	"github.com/phrond/evermock/internal/proxy"
)

type queuedResponse struct {
	ID           string
	Stub         mock.Stub
	CreatedAt    time.Time
	RespondedAt  *time.Time
	RespondedCnt int
	Source       string
	LastExchange *mock.Exchange
}

type runtimeSession struct {
	ID             string
	Name           string
	Store          *TestCaseStore
	SUTBaseURL     string
	MockBaseURL    string
	CreatedAt      time.Time
	QueueOrder     []string
	Queued         map[string]*queuedResponse
	RespondedOrder []string
	Responded      map[string]*queuedResponse
	SeenExchanges  map[string]struct{}
}

func (a *App) nextSessionID() string {
	a.sessionSeq++
	return fmt.Sprintf("session-%d-%d", time.Now().UTC().UnixNano(), a.sessionSeq)
}

func (a *App) recordProxyForSession(exchange proxy.HTTPExchange) {
	sessionID := strings.TrimSpace(exchange.SessionID)

	a.sessionsMu.Lock()
	if sessionID == "" && len(a.sessions) == 1 {
		for id := range a.sessions {
			sessionID = id
			break
		}
	}
	session := a.sessions[sessionID]
	a.sessionsMu.Unlock()

	if session == nil || session.Store == nil {
		return
	}
	session.Store.RecordHTTP(exchange)
}

func sessionPathPrefix(sessionID string) string {
	return "/__phrond/session/" + strings.TrimSpace(sessionID)
}

func withSessionPath(sessionID, path string) string {
	prefix := sessionPathPrefix(sessionID)
	if path == "" || path == "/" {
		return prefix + "/"
	}
	if strings.HasPrefix(path, "/") {
		return prefix + path
	}
	return prefix + "/" + path
}

func stripSessionPath(path string) (sessionID, trimmed string) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) < 3 {
		return "", path
	}
	if parts[0] != "__phrond" || parts[1] != "session" {
		return "", path
	}
	sessionID = strings.TrimSpace(parts[2])
	if sessionID == "" {
		return "", path
	}
	trimmed = "/"
	if len(parts) > 3 {
		trimmed += strings.Join(parts[3:], "/")
	}
	return sessionID, trimmed
}

func removeID(order []string, id string) []string {
	if len(order) == 0 {
		return order
	}
	out := order[:0]
	for _, entry := range order {
		if entry == id {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func (a *App) syncSessionResponsesLocked(session *runtimeSession) {
	if session == nil || a.mockComponent == nil {
		return
	}
	if session.SeenExchanges == nil {
		session.SeenExchanges = map[string]struct{}{}
	}
	recorder := a.mockComponent.Recorder()
	if recorder == nil {
		return
	}
	exchanges := recorder.Exchanges()
	for _, exchange := range exchanges {
		if exchange.SessionID != session.ID {
			continue
		}
		if exchange.ID != "" {
			if _, seen := session.SeenExchanges[exchange.ID]; seen {
				continue
			}
			session.SeenExchanges[exchange.ID] = struct{}{}
		}
		responseID := strings.TrimSpace(exchange.MatchedStubID)
		if responseID == "" {
			continue
		}

		item := session.Queued[responseID]
		if item != nil {
			if !item.Stub.Default {
				delete(session.Queued, responseID)
				session.QueueOrder = removeID(session.QueueOrder, responseID)
			}
		} else if existing := session.Responded[responseID]; existing != nil {
			item = existing
		} else {
			item = &queuedResponse{
				ID:        responseID,
				CreatedAt: exchange.StartedAt,
				Source:    "recorded",
				Stub: mock.Stub{
					ID:        responseID,
					SessionID: session.ID,
					Match: mock.StubMatch{
						Method: exchange.Request.Method,
						Path:   exchange.Request.Path,
						Body:   exchange.Request.Body,
					},
					Response: mock.StubResponse{
						Status:  exchange.Response.Status,
						Headers: singleValueHeaders(exchange.Response.Headers),
						Body:    exchange.Response.Body,
						DelayMS: exchange.Response.DelayMS,
						Fault:   exchange.Response.Fault,
					},
				},
			}
		}

		finished := exchange.FinishedAt
		item.RespondedAt = &finished
		item.RespondedCnt++
		exCopy := exchange
		item.LastExchange = &exCopy
		if _, exists := session.Responded[responseID]; !exists {
			session.RespondedOrder = append(session.RespondedOrder, responseID)
		}
		session.Responded[responseID] = item
	}
}

func singleValueHeaders(headers map[string][]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		out[key] = values[0]
	}
	return out
}

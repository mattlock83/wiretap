package explore

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
)

func TestExploreRejectsEventDependenciesBeforeSendingHTTP(t *testing.T) {
	_, original, _ := makeSeed(t, testSpec, nil)
	for _, role := range []string{"provided", "dependency"} {
		t.Run(role, func(t *testing.T) {
			flows := append([]artifact.Flow{}, original.Traffic...)
			flows = append(flows, artifact.Flow{ID: "events", Protocol: "kafka", Interaction: &artifact.Interaction{Kind: "publish", Boundary: artifact.BoundaryRef{Role: role, ID: "events"}, Transport: "kafka", Operation: "orders.created", CaptureState: "complete", Events: []artifact.InteractionEvent{{ID: "send", Direction: "sent", Kind: "message"}}}})
			source, _, _ := makeSeed(t, testSpec, flows)
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(201) }))
			defer server.Close()
			_, err := Run(t.Context(), Options{Bundle: source, Target: server.URL, Output: t.TempDir(), Logs: io.Discard})
			if err == nil || !strings.Contains(err.Error(), "event interaction") {
				t.Fatalf("expected event interaction refusal, got %v", err)
			}
			if requests.Load() != 0 {
				t.Fatal("unsupported replay sent HTTP requests")
			}
		})
	}
}

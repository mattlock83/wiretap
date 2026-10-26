package assumptions

import (
	"errors"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func unsupportedSelectionInteraction(id, role, boundary string) artifact.Flow {
	return artifact.Flow{ID: id, Protocol: "kafka", Interaction: &artifact.Interaction{
		Kind: "publish", Boundary: artifact.BoundaryRef{Role: role, ID: boundary},
		Transport: "kafka", Operation: "orders.created", CaptureState: "complete",
		Events: []artifact.InteractionEvent{
			{ID: "message", Sequence: 0, Direction: "sent", DirectionSequence: 0, Kind: "message", Payloads: []artifact.EventPayload{{Name: "value", Encoding: "json", MediaType: "application/json", Availability: "present", Body: &artifact.Body{Encoding: "json", Text: `{"order":"one"}`}}}},
			{ID: "complete", Sequence: 1, Direction: "sent", DirectionSequence: 1, Kind: "complete"},
		},
	}}
}

func TestCheckSelectedMockEventAssumptionsCannotDisappearBehindSupportedHTTP(t *testing.T) {
	consumer := checkCapture(false, checkFixtureFlow())
	consumer.Traffic = append(consumer.Traffic, unsupportedSelectionInteraction("stock-event", "dependency", "stock"))
	options := checkHTTPPair(t, []*artifact.Artifact{consumer}, []*artifact.Artifact{checkCapture(true, checkFixtureFlow())})
	for _, mode := range []string{"consumer", "provider"} {
		t.Run(mode, func(t *testing.T) {
			options.Mode = mode
			if _, err := Check(t.Context(), options); !errors.Is(err, ErrInput) {
				t.Fatalf("selected unsupported mixed traffic accepted: %v", err)
			}
		})
	}
}

func TestCheckEntirelyUnsupportedSelectionKeepsUnverifiedVerdict(t *testing.T) {
	consumer := &artifact.Artifact{Traffic: []artifact.Flow{unsupportedSelectionInteraction("stock-event", "dependency", "stock")}}
	options := checkHTTPPair(t, []*artifact.Artifact{consumer}, []*artifact.Artifact{checkCapture(true, checkFixtureFlow())})
	report := mustCheck(t, options)
	if report.Compatible || report.Counts.Assumptions != 1 || report.Counts.Unverified != 1 || len(report.Findings) != 1 || report.Findings[0].Code != "consumer_evidence_unavailable" {
		t.Fatalf("unsupported-only selection lost its verdict: %+v", report)
	}
}

func TestCheckUnsupportedEventsInOtherDependenciesDoNotExpandSelectedBoundary(t *testing.T) {
	identity := consumerCheckIdentity()
	identity.DependencyKinds["audit"] = "mock"
	consumer := checkCapture(false, checkFixtureFlow())
	consumer.Traffic = append(consumer.Traffic, unsupportedSelectionInteraction("audit-event", "dependency", "audit"))
	options := Options{Mode: "consumer", Dependency: "stock", Consumer: writeCheckArchive(t, identity, "complete", consumer), Provider: writeCheckArchive(t, providerCheckIdentity(), "complete", checkCapture(true, checkFixtureFlow()))}
	report := mustCheck(t, options)
	if !report.Compatible || report.Counts.Assumptions != 1 || report.Counts.Verified != 1 || len(report.Findings) != 0 {
		t.Fatalf("unrelated dependency changed the selected check: %+v", report)
	}
}

func TestCheckUnsupportedProviderEventsPreventPartialArazzoCertification(t *testing.T) {
	provider := checkCapture(true, checkFixtureFlow())
	provider.Traffic = append(provider.Traffic, unsupportedSelectionInteraction("provided-event", "provided", "inventory-api"))
	options := checkHTTPPair(t, []*artifact.Artifact{checkCapture(false, checkFixtureFlow())}, []*artifact.Artifact{provider})
	if _, err := Check(t.Context(), options); !errors.Is(err, ErrInput) {
		t.Fatalf("partly represented provider selection accepted: %v", err)
	}
}

package bundle

import (
	"strings"
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
)

func TestGRPCExplorationRequiresExplicitCapabilityAndNativeCodes(t *testing.T) {
	for name, change := range map[string]func(*Attempt, *Run){
		"valid catalogue":       func(*Attempt, *Run) {},
		"missing capability":    func(a *Attempt, r *Run) { a.RequiredCapabilities = []string{grpcObservationCapability} },
		"HTTP status":           func(a *Attempt, r *Run) { a.Capture.Exploration.Catalogue[0].ExpectedStatuses = []int{400} },
		"authentication status": func(a *Attempt, r *Run) { a.Capture.Exploration.Catalogue[0].ExpectedStatuses = []int{16} },
		"missing message index": func(a *Attempt, r *Run) { a.Capture.Exploration.Catalogue[0].MessageIndex = nil },
		"mismatched location":   func(a *Attempt, r *Run) { a.Capture.Exploration.Catalogue[0].Location = "/messages/1/body/name" },
	} {
		t.Run(name, func(t *testing.T) {
			root, attempt, run := grpcObservedBundleFixture(t)
			flow := attempt.Capture.Traffic[0]
			index := 0
			attempt.Capture.Exploration = &artifact.Exploration{Version: "wiretap-negative-1", Mode: "baseline", Catalogue: []artifact.RejectionTarget{{Key: strings.Repeat("a", 64), Protocol: "grpc", SchemaID: flow.Request.GRPC.SchemaRef, Operation: flow.Request.GRPC.Procedure, FlowID: flow.ID, MessageIndex: &index, Location: "/messages/0/body/name", ExpectedStatuses: []int{3}}}}
			attempt.RequiredCapabilities = requiredCapabilities(attempt.Capture.Schemas, []*artifact.Artifact{&attempt.Capture})
			run.RequiredCapabilities = append([]string(nil), attempt.RequiredCapabilities...)
			found := false
			for _, capability := range attempt.RequiredCapabilities {
				if capability == grpcExplorationCapability {
					found = true
				}
			}
			if !found {
				t.Fatal("gRPC catalogue omitted semantic capability")
			}
			change(&attempt, &run)
			rewriteGRPCAttempt(t, root, attempt, run)
			_, _, err := Load(root)
			if name == "valid catalogue" && err != nil {
				t.Fatal(err)
			}
			if name != "valid catalogue" && err == nil {
				t.Fatal("accepted invalid native exploration metadata")
			}
		})
	}
}

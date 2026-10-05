package bundle

import (
	"fmt"
	"strings"

	"github.com/phrond/wiretap/internal/artifact"
)

// A gRPC code is not an HTTP status. Keep this semantic distinction explicit
// even on failed cases, where the actual response may have any status.
func validateExplorationProtocols(capture *artifact.Artifact) error {
	x := capture.Exploration
	if x == nil {
		return nil
	}
	targets := append([]artifact.RejectionTarget(nil), x.Catalogue...)
	if x.Target != nil {
		targets = append(targets, *x.Target)
	}
	for _, target := range targets {
		switch target.Protocol {
		case "", "http":
			if target.MessageIndex != nil {
				return fmt.Errorf("HTTP rejection target cannot select a protobuf message")
			}
		case "grpc":
			if target.MessageIndex == nil || *target.MessageIndex < 0 {
				return fmt.Errorf("gRPC rejection target requires a message index")
			}
			prefix := fmt.Sprintf("/messages/%d/body", *target.MessageIndex)
			if target.Location != prefix && !strings.HasPrefix(target.Location, prefix+"/") {
				return fmt.Errorf("gRPC rejection target location disagrees with message index")
			}
			if len(target.ExpectedStatuses) == 0 {
				return fmt.Errorf("gRPC rejection target requires expected validation statuses")
			}
			for _, status := range target.ExpectedStatuses {
				if status != 3 && status != 11 {
					return fmt.Errorf("gRPC rejection target must expect INVALID_ARGUMENT or OUT_OF_RANGE")
				}
			}
		default:
			return fmt.Errorf("unsupported rejection target protocol %q", target.Protocol)
		}
	}
	if x.Target != nil {
		for _, flow := range capture.Traffic {
			if flow.ID != x.Target.FlowID {
				continue
			}
			if (x.Target.Protocol == "grpc") != (flow.Protocol == "grpc") {
				return fmt.Errorf("rejection target protocol disagrees with its flow")
			}
		}
	}
	return nil
}

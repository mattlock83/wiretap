package bundle

import (
	"encoding/base64"
	"fmt"
	"unicode/utf8"

	"github.com/phrond/wiretap/internal/artifact"
)

// ValidateInlineCapture applies recording semantics independently of archive
// filenames. The caller verifies record and contract digests before providing
// the frozen, offline contract resolver. No filesystem paths are guessed here.
func ValidateInlineCapture(capture *artifact.Artifact, readContract func(string) ([]byte, error)) error {
	if err := artifact.ValidateInteractions(capture); err != nil {
		return err
	}
	if err := validateGRPCMessages(EvidenceVersion, *capture); err != nil {
		return err
	}
	if err := verifyGRPCPayloadsUsing(*capture, readContract); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, flow := range capture.Traffic {
		for _, msg := range []artifact.Message{flow.Request, flow.Response} {
			if msg.HTTP != nil {
				if err := checkInlineBody(msg.HTTP.Body); err != nil {
					return err
				}
			}
			if msg.GRPC != nil {
				for _, payload := range msg.GRPC.Messages {
					if err := checkInlineBody(payload.Body); err != nil {
						return err
					}
					if err := checkInlineBody(payload.Wire); err != nil {
						return err
					}
				}
			}
		}
		if flow.Response.GRPC != nil && flow.Response.GRPC.StatusDetails != nil {
			for _, item := range statusBodies(flow.Response.GRPC.StatusDetails) {
				if item.body != nil {
					if err := checkInlineBody(item.body); err != nil {
						return err
					}
					seen[statusBodyKey(flow.ID, item.kind, item.index)] = true
				}
			}
		}
	}
	return verifyGRPCStatusUsing(*capture, seen, readContract)
}
func checkInlineBody(b *artifact.Body) error {
	if b == nil {
		return nil
	}
	if !utf8.ValidString(b.Text) || b.Text != "" && b.Bytes != "" {
		return fmt.Errorf("invalid inline text/binary representation")
	}
	if b.Bytes != "" {
		if _, err := base64.StdEncoding.DecodeString(b.Bytes); err != nil {
			return err
		}
	}
	return nil
}

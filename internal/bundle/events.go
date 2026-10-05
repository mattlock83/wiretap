package bundle

import (
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/phrond/wiretap/internal/artifact"
)

// Application identifies the tested software independently of API ownership.
// A UI may have an application identity and no provided APIs.
type Application struct {
	Name        string `json:"name,omitempty"`
	ID          string `json:"id,omitempty"`
	Description string `json:"description,omitempty"`
}

func validateApplication(app *Application) error {
	if app != nil && strings.TrimSpace(app.Name) == "" && strings.TrimSpace(app.ID) == "" {
		return errors.New("application requires a name or ID")
	}
	return nil
}

const eventCapability = "event-interactions.v1"
const packageCapability = "contract-packages.v1"

func requiredCapabilities(schemas []artifact.SchemaRef, captures []*artifact.Artifact) []string {
	result := []string{}
	for _, schema := range schemas {
		if opaqueContractType(schema.Type) {
			result = uniqueString(result, packageCapability)
		}
	}
	for _, capture := range captures {
		for _, schema := range capture.Schemas {
			if opaqueContractType(schema.Type) {
				result = uniqueString(result, packageCapability)
			}
		}
		for _, flow := range capture.Traffic {
			if flow.Interaction != nil {
				result = uniqueString(result, eventCapability)
			}
		}
	}
	sort.Strings(result)
	return result
}

func mergeCapabilities(left, right []string) []string {
	out := append([]string{}, left...)
	for _, value := range right {
		out = uniqueString(out, value)
	}
	sort.Strings(out)
	return out
}

func checkCapabilities(required []string) error {
	if required == nil {
		return errors.New("missing required_capabilities declaration")
	}
	seen := map[string]bool{}
	for _, capability := range required {
		if capability != eventCapability && capability != packageCapability {
			return fmt.Errorf("unsupported required capability %q", capability)
		}
		if seen[capability] {
			return fmt.Errorf("duplicate required capability %q", capability)
		}
		seen[capability] = true
	}
	return nil
}

func eventBodyKey(flow, event, payload string) string {
	return flow + "/event/" + event + "/" + payload
}

func (w *Writer) attachEvents(attempt *Attempt) error {
	for i := range attempt.Capture.Traffic {
		flow := &attempt.Capture.Traffic[i]
		if flow.Interaction == nil {
			continue
		}
		for j := range flow.Interaction.Events {
			event := &flow.Interaction.Events[j]
			for k := range event.Payloads {
				p := &event.Payloads[k]
				if p.Body == nil {
					continue
				}
				raw, representation := []byte(p.Body.Text), "text"
				if p.Body.Bytes != "" || p.Body.Encoding == "base64" {
					var err error
					raw, err = base64.StdEncoding.DecodeString(p.Body.Bytes)
					if err != nil {
						return err
					}
					representation = "bytes"
				}
				hash := digest(raw)
				rel := "payloads/" + hash + ".bin"
				if err := writeContent(w.root, rel, raw); err != nil {
					return err
				}
				attempt.Bodies = append(attempt.Bodies, BodyRef{FlowID: flow.ID, Phase: "event", EventID: event.ID, PayloadName: p.Name, Representation: representation, FileRef: FileRef{Path: rel, SHA256: hash}, RetainedBytes: len(raw)})
				p.Body.Text, p.Body.Bytes = "", ""
			}
		}
	}
	return nil
}

func restoreEventBody(root string, flows map[string]*artifact.Flow, seen map[string]bool, ref BodyRef) error {
	key := eventBodyKey(ref.FlowID, ref.EventID, ref.PayloadName)
	if seen[key] || ref.EventID == "" || ref.PayloadName == "" || ref.MessageIndex != nil || ref.Kind != "" {
		return fmt.Errorf("invalid or duplicate event attachment %q", key)
	}
	flow := flows[ref.FlowID]
	if flow == nil || flow.Interaction == nil {
		return fmt.Errorf("event attachment %q references missing interaction", key)
	}
	var body *artifact.Body
	for i := range flow.Interaction.Events {
		event := &flow.Interaction.Events[i]
		if event.ID != ref.EventID {
			continue
		}
		for j := range event.Payloads {
			if event.Payloads[j].Name == ref.PayloadName {
				body = event.Payloads[j].Body
				break
			}
		}
	}
	if body == nil || body.Text != "" || body.Bytes != "" || ref.RetainedBytes < 0 {
		return fmt.Errorf("event attachment %q has missing body or inline bytes", key)
	}
	if ref.Path != "payloads/"+ref.SHA256+".bin" {
		return fmt.Errorf("invalid event payload path %q", ref.Path)
	}
	raw, err := readRef(root, ref.FileRef)
	if err != nil {
		return err
	}
	if len(raw) != ref.RetainedBytes {
		return fmt.Errorf("event attachment %q size mismatch", key)
	}
	switch ref.Representation {
	case "text":
		if !utf8.Valid(raw) {
			return fmt.Errorf("event attachment %q text is not UTF-8", key)
		}
		if body.Encoding == "base64" {
			return fmt.Errorf("event attachment %q representation disagrees with encoding", key)
		}
		body.Text = string(raw)
	case "bytes":
		body.Bytes = base64.StdEncoding.EncodeToString(raw)
	default:
		return fmt.Errorf("unsupported event attachment representation %q", ref.Representation)
	}
	seen[key] = true
	return nil
}

func validateEventAttachments(capture artifact.Artifact, seen map[string]bool) error {
	for _, flow := range capture.Traffic {
		if flow.Interaction == nil {
			continue
		}
		for _, event := range flow.Interaction.Events {
			for _, payload := range event.Payloads {
				if payload.Body != nil && !seen[eventBodyKey(flow.ID, event.ID, payload.Name)] {
					return fmt.Errorf("event %q payload %q is missing a verified attachment", event.ID, payload.Name)
				}
			}
		}
	}
	return nil
}

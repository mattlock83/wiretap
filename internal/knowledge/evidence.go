package knowledge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/phrond/wiretap/internal/artifact"
	"github.com/phrond/wiretap/internal/bundle"
)

func validateEvidence(f map[string][]byte, m, pub Object, subjects, reg map[string]Object, contracts map[string]string, r *Validation) {
	sources := map[string]Object{}
	for _, s := range list(pub["sources"]) {
		s := object(s)
		id := str(s["id"])
		if sources[id] != nil {
			r.fail("application", "PUB-01", "publication.json", "duplicate source ID")
		}
		sources[id] = s
		if repository := str(s["repository"]); repository != "" {
			u, err := url.Parse(repository)
			if err != nil || u.User != nil {
				r.fail("application", "PUB-01", "publication.json", "source repository URI contains credentials")
			}
		}
	}
	checkSources := func(ids []any, p string) {
		roles := map[string]bool{}
		for _, id := range ids {
			s := sources[str(id)]
			if s == nil {
				r.fail("application", "PUB-02", p, "unknown source binding")
			}
			roles[str(s["role"])] = true
		}
		if !roles["application"] || !roles["tests"] {
			r.fail("application", "PUB-02", p, "application and tests source roles required")
		}
	}
	checkSources(list(pub["target_source_ids"]), "publication.json")
	selected := map[string]bool{}
	selection := object(pub["selection"])
	for _, id := range list(selection["test_ids"]) {
		id := str(id)
		if str(subjects[id]["kind"]) != "test" {
			r.fail("application", "PUB-03", "publication.json", "selected ID is not a test")
		}
		selected[id] = true
	}
	if str(selection["scope"]) == "complete" {
		for id, s := range subjects {
			if s["kind"] == "test" && s["status"] == "active" && !selected[id] {
				r.fail("application", "PUB-03", "publication.json", "complete selection omits active test")
			}
		}
	}
	for _, s := range subjects {
		if source := object(s["source"]); source != nil && sources[str(source["source_id"])] == nil {
			r.fail("application", "MAN-05", str(s["page"]), "unknown subject source")
		}
	}
	if len(list(m["evidence"])) != 0 {
		r.Evidence = "valid"
	}
	seen := map[[2]string]bool{}
	executions := map[[2]string]bool{}
	ordinals := map[[3]string][]int{}
	groupSources := map[[3]string]string{}
	observed := map[string]bool{}
	for _, x := range list(m["evidence"]) {
		x := object(x)
		p := str(x["path"])
		tid := str(x["test_id"])
		s := subjects[tid]
		slot := str(x["slot"])
		group := str(x["group"])
		if s["kind"] != "test" || !safeKey(slot) || !safeKey(group) || p != "evidence/tests/"+str(s["key"])+"/"+slot+"/attempt.json" {
			r.fail("evidence", "PUB-04", p, "invalid test slot binding")
		}
		if seen[[2]string{tid, slot}] {
			r.fail("evidence", "PUB-04", p, "duplicate evidence slot")
		}
		seen[[2]string{tid, slot}] = true
		observed[tid] = true
		if reg[p] == nil || reg[p]["role"] != "evidence" {
			r.fail("evidence", "MAN-05", p, "unregistered attempt")
		}
		v, err := Decode(f[p])
		if err != nil {
			r.fail("evidence", "EVD-02", p, err)
			continue
		}
		if err := schemaCheck("test-result.schema.json", v); err != nil {
			r.fail("evidence", "EVD-02", p, err)
			continue
		}
		a := object(v)
		if err := coreNumbers(a); err != nil {
			r.fail("evidence", "DIFF-02", p, err)
		}
		if a["test_id"] != tid || a["execution_group"] != group {
			r.fail("evidence", "PUB-04", p, "attempt identity binding mismatch")
		}
		checkSources(list(a["source_ids"]), p)
		start, err := time.Parse(time.RFC3339Nano, str(a["started_at"]))
		if err != nil {
			r.fail("evidence", "EVD-02", p, err)
		}
		if finish := str(a["finished_at"]); finish != "" {
			end, err := time.Parse(time.RFC3339Nano, finish)
			if err != nil || end.Before(start) {
				r.fail("evidence", "EVD-02", p, "finish precedes start")
			}
		} else if a["capture_state"] == "complete" || (a["outcome"] != "error" && a["outcome"] != "unknown") {
			r.fail("evidence", "EVD-02", p, "unfinished attempt cannot be complete or successful")
		}
		if a["capture_state"] != "complete" && len(list(a["diagnostics"])) == 0 {
			r.fail("evidence", "EVD-02", p, "incomplete capture requires diagnostics")
		}
		for _, id := range list(object(a["capture_scope"])["subject_ids"]) {
			kind := str(subjects[str(id)]["kind"])
			if kind != "operation" && kind != "provided-api" && kind != "dependency" {
				r.fail("evidence", "EVD-02", p, "capture scope is not a boundary/operation")
			}
		}
		bindings := map[string]string{}
		for _, c := range list(a["contracts"]) {
			c := object(c)
			id := str(c["contract_id"])
			if bindings[id] != "" || contracts[id] == "" || contracts[id] != str(c["manifest_sha256"]) {
				r.fail("evidence", "EVD-03", p, "invalid exact contract binding")
			}
			bindings[id] = str(c["manifest_sha256"])
		}
		exec := [2]string{str(a["run_id"]), str(a["attempt_id"])}
		if executions[exec] {
			r.fail("evidence", "ID-08", p, "duplicate execution representation")
		}
		executions[exec] = true
		g := [3]string{tid, str(a["run_id"]), group}
		ordinals[g] = append(ordinals[g], number(a["attempt_ordinal"]))
		sourceIDs := append([]any{}, list(a["source_ids"])...)
		sort.Slice(sourceIDs, func(i, j int) bool { return str(sourceIDs[i]) < str(sourceIDs[j]) })
		sourceBytes, _ := Canonical(sourceIDs)
		if old := groupSources[g]; old != "" && old != string(sourceBytes) {
			r.fail("evidence", "PUB-04", p, "group source bindings differ")
		}
		groupSources[g] = string(sourceBytes)
		artNames := map[string]bool{}
		for _, y := range list(a["artifacts"]) {
			y := object(y)
			ap := str(y["path"])
			name := str(y["name"])
			if artNames[name] || !safeKey(name) {
				r.fail("evidence", "EVD-04", ap, "artifact name collision/unsafe key")
			}
			artNames[name] = true
			if reg[ap] == nil || reg[ap]["role"] != "evidence" || reg[ap]["sha256"] != y["sha256"] || reg[ap]["media_type"] != y["media_type"] {
				r.fail("evidence", "MAN-05", ap, "artifact registry mismatch")
			}
			if y["kind"] == "payload" {
				retention := object(y["retention"])
				if number(retention["retained_bytes"]) != len(f[ap]) || (retention["original_bytes"] != nil && number(retention["original_bytes"]) < len(f[ap])) {
					r.fail("evidence", "EVD-07", ap, "payload retention size mismatch")
				}
			}
			if profile := object(y["profile"]); profile != nil {
				if profile["id"] == RecordingID && profile["version"] == RecordingVersion {
					if err := validateRecording(f[ap], a, bindings, reg, f); err != nil {
						r.fail("evidence", "EVD-04", ap, err)
					}
				} else {
					r.OptionalProfiles = append(r.OptionalProfiles, str(profile["id"])+"@"+str(profile["version"]))
				}
			}
		}
	}
	for _, os := range ordinals {
		sort.Ints(os)
		for i, n := range os {
			if i != n {
				r.fail("evidence", "PUB-04", "bundle.json", "attempt ordinals must be contiguous from zero")
			}
		}
	}
	for id := range selected {
		if !observed[id] {
			r.fail("evidence", "PUB-05", "publication.json", "selected test has no result: "+id)
		}
	}
}

func validateRecording(b []byte, envelope Object, bindings map[string]string, reg map[string]Object, files map[string][]byte) error {
	v, err := Decode(b)
	if err != nil {
		return err
	}
	record := object(v)
	if err := schemaCheck("wiretap-recording.schema.json", record); err != nil {
		return err
	}
	if record["run_id"] != envelope["run_id"] || record["attempt_id"] != envelope["attempt_id"] {
		return fmt.Errorf("recording execution binding mismatch")
	}
	if transformation := object(envelope["transformation"]); transformation != nil && transformation["source_record_sha256"] != record["source_record_sha256"] {
		return fmt.Errorf("recording transformation source digest mismatch")
	}
	capture := object(record["capture"])
	tc := object(capture["testcase"])
	if tc["id"] != envelope["attempt_id"] || tc["started_at"] != envelope["started_at"] || tc["finished_at"] != envelope["finished_at"] {
		return fmt.Errorf("recording test identity/time mismatch")
	}
	seenSchemas := map[string]bool{}
	for _, s := range list(capture["schemas"]) {
		s := object(s)
		if seenSchemas[str(s["id"])] {
			return fmt.Errorf("ambiguous recording schema identity")
		}
		seenSchemas[str(s["id"])] = true
		source := object(s["source"])
		if source["type"] == "omitted" {
			continue
		}
		p := str(source["path"])
		if !safePath(p) || reg[p] == nil || reg[p]["role"] != "contract" || source["sha256"] != reg[p]["sha256"] {
			return fmt.Errorf("recording contract file is not pinned")
		}
		id := str(object(record["contract_ids"])[str(s["id"])])
		if bindings[id] == "" {
			return fmt.Errorf("recording contract lacks attempt binding")
		}
		matched := false
		for mp, entry := range reg {
			if entry["role"] != "contract" || entry["sha256"] != bindings[id] || path.Base(mp) != "manifest.json" {
				continue
			}
			cm, err := Decode(files[mp])
			if err != nil {
				return err
			}
			cmObject := object(cm)
			if cmObject["contract_id"] == id && cmObject["format"] == s["type"] && path.Join(path.Dir(mp), str(cmObject["entrypoint"])) == p {
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("recording schema is bound to a different contract package")
		}
	}
	data, err := Canonical(capture)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var a artifact.Artifact
	if err := d.Decode(&a); err != nil {
		return err
	}
	if envelope["outcome"] != outcome(a.TestCase.Status) && !(a.TestCase.FinishedAt.IsZero() && envelope["outcome"] == "unknown") {
		return fmt.Errorf("recording outcome disagrees with neutral envelope")
	}
	if envelope["capture_state"] == "complete" && capturePartial(&a) {
		return fmt.Errorf("recording limitations disagree with complete capture claim")
	}
	return bundle.ValidateInlineCapture(&a, func(p string) ([]byte, error) {
		if !safePath(p) || reg[p] == nil || reg[p]["role"] != "contract" {
			return nil, fmt.Errorf("unverified recording contract reference")
		}
		data, ok := files[p]
		if !ok || hash(data) != reg[p]["sha256"] {
			return nil, fmt.Errorf("missing/corrupt recording contract")
		}
		return data, nil
	})
}

func recordingPath(attemptPath string) string {
	return path.Join(path.Dir(attemptPath), "recording.json")
}
func outcome(status string) string {
	switch strings.ToLower(status) {
	case "pass", "passed", "success", "ok":
		return "pass"
	case "fail", "failed", "failure":
		return "fail"
	case "skip", "skipped":
		return "skip"
	case "error":
		return "error"
	default:
		return "unknown"
	}
}

func capturePartial(a *artifact.Artifact) bool {
	if a.CaptureIncomplete {
		return true
	}
	for _, flow := range a.Traffic {
		if flow.Interaction != nil {
			if flow.Interaction.CaptureState != "complete" {
				return true
			}
			for _, event := range flow.Interaction.Events {
				for _, payload := range event.Payloads {
					if payload.Availability == "unavailable" || payload.Availability == "truncated" || payload.Availability == "redacted" {
						return true
					}
				}
			}
		}
		for _, msg := range []artifact.Message{flow.Request, flow.Response} {
			if msg.HTTP != nil && msg.HTTP.Body != nil && msg.HTTP.Body.Truncated {
				return true
			}
			if msg.GRPC != nil {
				if status := msg.GRPC.StatusDetails; status != nil && (status.State == "truncated" || status.State == "redacted") {
					return true
				}
				if msg.GRPC.Observation != nil && (msg.GRPC.Observation.OmittedFrames > 0 || msg.GRPC.Observation.End == "open") {
					return true
				}
				for _, payload := range msg.GRPC.Messages {
					if payload.Redacted || payload.Body != nil && payload.Body.Truncated || payload.Wire != nil && payload.Wire.Truncated {
						return true
					}
				}
			}
		}
	}
	return false
}

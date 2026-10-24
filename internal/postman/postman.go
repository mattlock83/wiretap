// Package postman exports captured HTTP requests as Postman v2.1 collections
// and native gRPC conversations as Postman v3 collections.
package postman

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/mattlock83/wiretap/internal/artifact"
	"github.com/mattlock83/wiretap/internal/bundle"
)

const collectionSchema = "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"

// Generate exports the SUT and each mock dependency with captured HTTP
// requests. Real services, unknown target kinds and gRPC traffic are excluded.
// It verifies the bundle once so every collection describes the same snapshot.
// Each committed test attempt gets a folder, even when it has no HTTP requests.
func Generate(bundlePath, sutName string) ([]Export, error) {
	run, captures, err := loadRun(bundlePath)
	if err != nil {
		return nil, err
	}
	return generateHTTP(run, captures, sutName)
}

func generateHTTP(run *bundle.Run, captures []*artifact.Artifact, sutName string) ([]Export, error) {
	if strings.TrimSpace(sutName) == "" {
		sutName = collectionName(run)
	}
	sut, err := generateCollection(run, captures, sutName, "test_to_sut", "")
	if err != nil {
		return nil, err
	}
	usedFilenames := map[string]bool{}
	exports := []Export{{Filename: exportFilename("sut", applicationID(run), usedFilenames), Collection: sut}}
	mocks := map[string]bool{}
	for _, capture := range captures {
		for _, flow := range capture.Traffic {
			if !isHTTPRequest(flow) || flow.Channel != "sut_to_dependency" {
				continue
			}
			if flow.AssetID != "" && run.DependencyKinds[flow.AssetID] == "mock" {
				mocks[flow.AssetID] = true
			}
		}
	}
	ids := make([]string, 0, len(mocks))
	for id := range mocks {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		collection, err := generateCollection(run, captures, mockCollectionName(run, id), "sut_to_dependency", id)
		if err != nil {
			return nil, err
		}
		exports = append(exports, Export{Filename: exportFilename("mock", id, usedFilenames), Collection: collection})
	}
	return exports, nil
}

func loadRun(bundlePath string) (*bundle.Run, []*artifact.Artifact, error) {
	run, captures, err := bundle.Load(bundlePath)
	if err != nil {
		return nil, nil, fmt.Errorf("export Postman collection: %w", err)
	}
	// Test start order is useful when the manifest lists attempts by completion.
	allStarted := true
	for _, capture := range captures {
		allStarted = allStarted && !capture.TestCase.StartedAt.IsZero()
	}
	if allStarted {
		sort.SliceStable(captures, func(i, j int) bool {
			return captures[i].TestCase.StartedAt.Before(captures[j].TestCase.StartedAt)
		})
	}
	return run, captures, nil
}

func generateCollection(run *bundle.Run, captures []*artifact.Artifact, name, channel, assetID string) (*Collection, error) {
	scope := "from tests to the SUT"
	if channel == "sut_to_dependency" {
		scope = "from the SUT to mock dependency " + assetID
	}
	collection := &Collection{
		Info: Info{
			Name: name, Schema: collectionSchema,
			Description: "Captured HTTP requests " + scope + ". Run: " + run.RunID + ". " +
				"Each folder represents one test attempt. URLs, payloads and expected status codes " +
				"come from captured traffic and retain synthetic test data. Capture limits still apply. gRPC traffic is excluded.",
		},
		Items: make([]Folder, 0, len(captures)),
		// Preserve the captured requests: redirects are separate exchanges,
		// cookies and other application headers are explicit, and URLs are encoded.
		ProtocolProfileBehavior: ProtocolBehavior{
			FollowRedirects: false, DisableBodyPruning: true,
			DisableCookies: true, DisableURLEncoding: true,
			DisabledSystemHeaders: map[string]bool{
				"content-type": true, "accept": true, "accept-encoding": true,
				"user-agent": true, "cache-control": true, "postman-token": true,
			},
		},
	}
	labels := attemptFolderNames(captures)
	for _, capture := range captures {
		folder, err := exportTest(capture, channel, assetID)
		if err != nil {
			return nil, fmt.Errorf("export Postman test %q: %w", capture.TestCase.ID, err)
		}
		folder.ID, folder.Name = capture.TestCase.ID, labels[capture.TestCase.ID]
		collection.Items = append(collection.Items, folder)
	}
	return collection, nil
}

// Write emits an importable collection to a file or stdout.
func Write(w io.Writer, collection *Collection) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(collection)
}

func isHTTPRequest(flow artifact.Flow) bool {
	return flow.Protocol == "http" && flow.Request.HTTP != nil && flow.Interaction == nil
}

func exportTest(capture *artifact.Artifact, channel, assetID string) (Folder, error) {
	name := capture.TestCase.Name
	if name == "" {
		name = capture.TestCase.ID
	}
	folder := Folder{Name: name, Description: "Test attempt: " + capture.TestCase.ID + ".", Items: []Item{}}
	if capture.CaptureIncomplete {
		folder.Description += " Capture incomplete; only retained traffic is exported."
	}
	flows := []artifact.Flow{}
	chronological := true
	for _, flow := range capture.Traffic {
		if !isHTTPRequest(flow) || flow.Channel != channel {
			continue
		}
		if assetID != "" && flow.AssetID != assetID {
			continue
		}
		flows = append(flows, flow)
		chronological = chronological && !flow.StartedAt.IsZero()
	}
	if chronological {
		sort.SliceStable(flows, func(i, j int) bool { return flows[i].StartedAt.Before(flows[j].StartedAt) })
	} else {
		folder.Description += " Request start timestamps are missing; chronological order is unavailable. Retained capture order is used."
	}
	for _, flow := range flows {
		item, err := exportFlow(flow)
		if err != nil {
			return Folder{}, fmt.Errorf("flow %q: %w", flow.ID, err)
		}
		folder.Items = append(folder.Items, item)
	}
	return folder, nil
}

func exportFlow(flow artifact.Flow) (Item, error) {
	req := flow.Request.HTTP
	request := Request{Method: req.Method, URL: req.URL, Description: "Captured flow: " + flow.ID + "."}
	text, binary, err := bodyText(req.Body)
	if err != nil {
		return Item{}, err
	}
	if req.Body != nil {
		if binary {
			// Postman decodes inline file content from base64 when src is null.
			request.Body = &RequestBody{Mode: "file", File: &File{Content: text}}
		} else {
			// Explicit text mode prevents Postman from stripping JSON comments
			// in malformed payloads captured by negative tests.
			request.Body = &RequestBody{
				Mode: "raw", Raw: &text,
				Options: &BodyOptions{Raw: RawOptions{Language: "text"}},
			}
		}
		if req.Body.Truncated {
			request.Description += " Request body was truncated during capture."
		}
	}
	request.Headers = requestHeaders(req)
	if flow.TransportError != "" {
		request.Description += " Capture transport error: " + flow.TransportError
	}
	item := Item{Name: req.Method + " " + req.URL, Request: request, Responses: []Response{}}
	res := flow.Response.HTTP
	status := 0
	if res != nil {
		if res.Status >= 100 && res.Status <= 999 {
			status = res.Status
		}
		body, binary, err := bodyText(res.Body)
		if err != nil {
			return Item{}, err
		}
		name := fmt.Sprintf("Captured response: %d", status)
		if status == 0 {
			name = "Captured partial response (status unavailable)"
		}
		if binary {
			name += " (body is base64)"
		}
		if res.Body != nil && res.Body.Truncated {
			name += " (body truncated during capture)"
		}
		item.Responses = append(item.Responses, Response{
			Name: name, OriginalRequest: request, Code: status, Status: http.StatusText(status),
			Headers: headers(res.Headers, nil), Body: body, ResponseTime: flow.DurationMS,
		})
	}
	lines := []string{`pm.test.skip("Status code unavailable in capture", function () {});`}
	if status != 0 {
		lines = []string{
			fmt.Sprintf(`pm.test("Status code is %d", function () {`, status),
			fmt.Sprintf("    pm.response.to.have.status(%d);", status),
			"});",
		}
	}
	item.Events = []Event{{Listen: "test", Script: Script{Type: "text/javascript", Exec: lines}}}
	return item, nil
}

func bodyText(body *artifact.Body) (string, bool, error) {
	if body == nil {
		return "", false, nil
	}
	if body.Bytes == "" {
		return body.Text, false, nil
	}
	raw, err := base64.StdEncoding.DecodeString(body.Bytes)
	if err != nil {
		return "", false, fmt.Errorf("decode captured body: %w", err)
	}
	if utf8.Valid(raw) {
		return string(raw), false, nil
	}
	return body.Bytes, true, nil
}

func requestHeaders(req *artifact.HttpMessage) []Header {
	// Postman computes framing for the retained body.
	omit := map[string]bool{
		"host": true, "content-length": true, "transfer-encoding": true,
		"connection": true, "proxy-connection": true, "trailer": true,
		"te": true, "upgrade": true,
	}
	for name, values := range req.Headers {
		if strings.EqualFold(name, "Connection") {
			for _, value := range values {
				for _, token := range strings.Split(value, ",") {
					omit[strings.ToLower(strings.TrimSpace(token))] = true
				}
			}
		}
	}
	if req.Body != nil && req.Body.ContentEncoding != "" {
		// The retained payload was decoded; its original encoding no longer applies.
		omit["content-encoding"] = true
	}
	return headers(req.Headers, omit)
}

func headers(source artifact.Headers, omit map[string]bool) []Header {
	names := make([]string, 0, len(source))
	for name := range source {
		if !omit[strings.ToLower(name)] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	result := []Header{}
	for _, name := range names {
		for _, value := range source[name] {
			result = append(result, Header{Key: name, Value: value})
		}
	}
	return result
}

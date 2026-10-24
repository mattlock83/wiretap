package assumptions

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
)

func httpNormalizationFixture() artifact.Flow {
	return artifact.Flow{ID: "flow", Protocol: "http", Channel: "sut_to_dependency", AssetID: "stock",
		Request: artifact.Message{HTTP: &artifact.HttpMessage{
			Method: "POST", URL: "http://127.0.0.1:1234/items/a%2Fb?q=one&a=first&a=second",
			Headers: artifact.Headers{"Content-Type": {"application/json"}, "Authorization": {"Bearer dummy"}, "Traceparent": {"first"}, "User-Agent": {"test-client"}},
			Body:    &artifact.Body{Text: `{"amount":1,"items":[1,null]}`},
		}},
		Response: artifact.Message{HTTP: &artifact.HttpMessage{
			Status: 200, Headers: artifact.Headers{"Content-Type": {"application/json"}, "Date": {"yesterday"}, "X-Business": {"one", "two"}},
			Trailers: artifact.Headers{"X-Result": {"done"}}, Body: &artifact.Body{Text: `{"available":true,"status":"pending","items":[1,2]}`},
		}},
	}
}

func cloneNormalizationFlow(t *testing.T, flow artifact.Flow) artifact.Flow {
	t.Helper()
	encoded, err := json.Marshal(flow)
	if err != nil {
		t.Fatal(err)
	}
	var result artifact.Flow
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func normalizedSignatures(t *testing.T, flow artifact.Flow, ignored ...string) (string, string) {
	t.Helper()
	req, res, err := normalizeFlow(flow, ignored)
	if err != nil {
		t.Fatal(err)
	}
	return req, res
}

func TestNormalizeHTTPTransportAndRepresentationEquivalence(t *testing.T) {
	base := httpNormalizationFixture()
	other := cloneNormalizationFlow(t, base)
	other.ID, other.Channel, other.AssetID = "other-flow", "test_to_sut", ""
	other.Request.HTTP.URL = "https://provider.example:443/items/a%2Fb?a=first&a=second&q=one"
	other.Request.HTTP.Headers = artifact.Headers{"content-type": {"application/json"}, "authorization": {"Bearer dummy"}, "traceparent": {"second"}, "user-agent": {"other-client"}, "Content-Length": {"999"}, "Connection": {"x-hop"}, "X-Hop": {"hop-specific"}}
	other.Request.HTTP.Body.Text = " {\"items\":[1.0,null],\"amount\":1e0}\n"
	other.Response.HTTP.Headers["Date"] = []string{"tomorrow"}
	other.Response.HTTP.Headers["Server"] = []string{"other-provider"}
	other.Response.HTTP.Body.Text = `{"items":[1e0,2.00],"status":"pending","available":true}`
	req, res := normalizedSignatures(t, base)
	otherReq, otherRes := normalizedSignatures(t, other)
	if req != otherReq || res != otherRes {
		t.Fatalf("transport or JSON notation changed signatures:\n%s\n%s\n%s\n%s", req, otherReq, res, otherRes)
	}
	if !strings.Contains(res, `"value":{"available":true,"items":[1,2],"status":"pending"}`) {
		t.Fatalf("body JSON is not available as structured report evidence: %s", res)
	}
}

func TestNormalizeHTTPPreservesRequestAssumptions(t *testing.T) {
	base := httpNormalizationFixture()
	baseReq, _ := normalizedSignatures(t, base)
	for _, tc := range []struct {
		name string
		edit func(*artifact.HttpMessage)
	}{
		{"method", func(m *artifact.HttpMessage) { m.Method = "PUT" }},
		{"method case", func(m *artifact.HttpMessage) { m.Method = "post" }},
		{"escaped slash", func(m *artifact.HttpMessage) { m.URL = "http://provider/items/a/b?q=one&a=first&a=second" }},
		{"query value", func(m *artifact.HttpMessage) { m.URL = "http://provider/items/a%2Fb?q=two&a=first&a=second" }},
		{"repeated query order", func(m *artifact.HttpMessage) { m.URL = "http://provider/items/a%2Fb?q=one&a=second&a=first" }},
		{"query presence", func(m *artifact.HttpMessage) { m.URL += "&optional=" }},
		{"authorization", func(m *artifact.HttpMessage) { m.Headers["Authorization"] = []string{"Bearer other-dummy"} }},
		{"body value", func(m *artifact.HttpMessage) { m.Body.Text = `{"amount":2,"items":[1,null]}` }},
		{"body type", func(m *artifact.HttpMessage) { m.Body.Text = `{"amount":"1","items":[1,null]}` }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := cloneNormalizationFlow(t, base)
			tc.edit(other.Request.HTTP)
			request, _ := normalizedSignatures(t, other)
			if request == baseReq {
				t.Fatal("different request assumption received an identical signature")
			}
		})
	}
}

func TestNormalizeHTTPPreservesResponseAssumptions(t *testing.T) {
	base := httpNormalizationFixture()
	baseReq, baseRes := normalizedSignatures(t, base)
	for _, tc := range []struct {
		name string
		edit func(*artifact.HttpMessage)
	}{
		{"status", func(m *artifact.HttpMessage) { m.Status = 404 }},
		{"boolean value", func(m *artifact.HttpMessage) { m.Body.Text = `{"available":false,"status":"pending","items":[1,2]}` }},
		{"enum value", func(m *artifact.HttpMessage) { m.Body.Text = `{"available":true,"status":"cancelled","items":[1,2]}` }},
		{"null versus missing", func(m *artifact.HttpMessage) {
			m.Body.Text = `{"available":true,"status":"pending","items":[1,2],"note":null}`
		}},
		{"array order", func(m *artifact.HttpMessage) { m.Body.Text = `{"available":true,"status":"pending","items":[2,1]}` }},
		{"array cardinality", func(m *artifact.HttpMessage) { m.Body.Text = `{"available":true,"status":"pending","items":[1]}` }},
		{"application header", func(m *artifact.HttpMessage) { m.Headers["X-Business"] = []string{"different"} }},
		{"repeated header order", func(m *artifact.HttpMessage) { m.Headers["X-Business"] = []string{"two", "one"} }},
		{"application trailer", func(m *artifact.HttpMessage) { m.Trailers["X-Result"] = []string{"incomplete"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := cloneNormalizationFlow(t, base)
			tc.edit(other.Response.HTTP)
			request, response := normalizedSignatures(t, other)
			if request != baseReq || response == baseRes {
				t.Fatal("response change was lost or changed request scenario identity")
			}
		})
	}
}

func TestNormalizeExactNumbersAndNonJSON(t *testing.T) {
	for _, pair := range [][2]string{
		{`{"n":100.00}`, `{"n":1e2}`},
		{`{"n":-0.0}`, `{"n":0}`},
		{`{"n":90071992547409931234567890}`, `{"n":9007199254740993123456789e1}`},
		{`{"n":0.00010000}`, `{"n":1e-4}`},
		{`{"n":1e999999999}`, `{"n":10e999999998}`},
	} {
		left, right := httpNormalizationFixture(), httpNormalizationFixture()
		left.Request.HTTP.Body.Text, right.Request.HTTP.Body.Text = pair[0], pair[1]
		leftReq, _ := normalizedSignatures(t, left)
		rightReq, _ := normalizedSignatures(t, right)
		if leftReq != rightReq {
			t.Fatalf("equivalent precise numbers differ: %s / %s", pair[0], pair[1])
		}
	}
	for _, pair := range [][2]string{
		{`{"n":9007199254740992}`, `{"n":9007199254740993}`},
		{`plain text`, `plain text\n`},
		{`{"a":1,"a":2}`, `{"a":2}`},
		{`{"a":1}garbage`, `{"a":1}`},
	} {
		left, right := httpNormalizationFixture(), httpNormalizationFixture()
		left.Request.HTTP.Body.Text, right.Request.HTTP.Body.Text = pair[0], pair[1]
		leftReq, _ := normalizedSignatures(t, left)
		rightReq, _ := normalizedSignatures(t, right)
		if leftReq == rightReq {
			t.Fatalf("meaningful retained bytes collapsed: %s / %s", pair[0], pair[1])
		}
	}
	absent, empty := httpNormalizationFixture(), httpNormalizationFixture()
	absent.Request.HTTP.Body, empty.Request.HTTP.Body = nil, &artifact.Body{}
	absentReq, _ := normalizedSignatures(t, absent)
	emptyReq, _ := normalizedSignatures(t, empty)
	if absentReq == emptyReq {
		t.Fatal("absent body collapsed into a retained empty body")
	}
	binary, text := httpNormalizationFixture(), httpNormalizationFixture()
	binary.Request.HTTP.Body = &artifact.Body{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString([]byte{0, 255, 1})}
	text.Request.HTTP.Body = &artifact.Body{Text: string([]byte{0, 255, 1})}
	binaryReq, _ := normalizedSignatures(t, binary)
	textReq, _ := normalizedSignatures(t, text)
	if binaryReq != textReq {
		t.Fatal("retained byte storage encoding changed application bytes")
	}
}

func TestNormalizeExplicitHeaderExceptions(t *testing.T) {
	left, right := httpNormalizationFixture(), httpNormalizationFixture()
	right.Request.HTTP.Headers["Authorization"] = []string{"Bearer other-dummy"}
	right.Response.HTTP.Headers["X-Business"] = []string{"different"}
	leftReq, leftRes := normalizedSignatures(t, left, "AuThOrIzAtIoN", "x-business")
	rightReq, rightRes := normalizedSignatures(t, right, "AuThOrIzAtIoN", "x-business")
	if leftReq != rightReq || leftRes != rightRes {
		t.Fatal("explicit header exceptions did not apply to both directions case insensitively")
	}
}

func TestNormalizePreservesMalformedUnicodeAndSupportsSurrogatePairs(t *testing.T) {
	for _, pair := range [][2]string{
		{`{"value":"\ud800"}`, `{"value":"\ud801"}`},
		{`{"value":"\ud800"}`, `{"value":"�"}`},
		{`{"value":"\udc00"}`, `{"value":"�"}`},
		{"{\"value\":\"" + string([]byte{0xff}) + "\"}", "{\"value\":\"" + string([]byte{0xfe}) + "\"}"},
	} {
		left, right := httpNormalizationFixture(), httpNormalizationFixture()
		left.Request.HTTP.Body.Text, right.Request.HTTP.Body.Text = pair[0], pair[1]
		leftReq, _ := normalizedSignatures(t, left)
		rightReq, _ := normalizedSignatures(t, right)
		if leftReq == rightReq {
			t.Fatalf("malformed Unicode examples collapsed into replacement characters: %q / %q", pair[0], pair[1])
		}
	}
	left, right := httpNormalizationFixture(), httpNormalizationFixture()
	left.Request.HTTP.Body.Text, right.Request.HTTP.Body.Text = `{"value":"\ud83d\ude00"}`, `{"value":"😀"}`
	leftReq, _ := normalizedSignatures(t, left)
	rightReq, _ := normalizedSignatures(t, right)
	if leftReq != rightReq {
		t.Fatal("valid surrogate pair did not normalize to the same Unicode string")
	}
	if !validJSONUnicode([]byte(`{"value":"\\ud800"}`)) {
		t.Fatal("an escaped literal backslash was incorrectly treated as a surrogate escape")
	}
}

func TestNormalizeRefusesUnavailableEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*artifact.Flow)
	}{
		{"request missing", func(f *artifact.Flow) { f.Request.HTTP = nil }},
		{"response missing", func(f *artifact.Flow) { f.Response.HTTP = nil }},
		{"status missing", func(f *artifact.Flow) { f.Response.HTTP.Status = 0 }},
		{"request truncated", func(f *artifact.Flow) { f.Request.HTTP.Body.Truncated = true }},
		{"response omitted", func(f *artifact.Flow) { f.Response.HTTP.Body.Omitted = true }},
		{"legacy redaction", func(f *artifact.Flow) { f.Response.HTTP.Body.Redacted = true }},
		{"missing retained bytes", func(f *artifact.Flow) { f.Response.HTTP.Body = &artifact.Body{OriginalBytes: 12} }},
		{"invalid base64", func(f *artifact.Flow) { f.Response.HTTP.Body = &artifact.Body{Bytes: "invalid!"} }},
		{"transport error", func(f *artifact.Flow) { f.TransportError = "connection reset" }},
		{"unsupported protocol", func(f *artifact.Flow) { f.Protocol = "graphql" }},
		{"event interaction", func(f *artifact.Flow) { f.Interaction = &artifact.Interaction{} }},
		{"invalid URL", func(f *artifact.Flow) { f.Request.HTTP.URL = "http://bad/%zz" }},
		{"URL credentials", func(f *artifact.Flow) { f.Request.HTTP.URL = "http://dummy:secret@provider/items" }},
		{"URL fragment", func(f *artifact.Flow) { f.Request.HTTP.URL = "http://provider/items#not-on-the-wire" }},
		{"URL missing host", func(f *artifact.Flow) { f.Request.HTTP.URL = "http:///items" }},
		{"invalid method token", func(f *artifact.Flow) { f.Request.HTTP.Method = "GET;POST" }},
		{"invalid query", func(f *artifact.Flow) { f.Request.HTTP.URL = "http://provider/items?q=%zz" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flow := httpNormalizationFixture()
			tc.edit(&flow)
			if _, _, err := normalizeFlow(flow, nil); err == nil {
				t.Fatal("unavailable evidence was accepted")
			}
		})
	}
}

func grpcNormalizationFixture() artifact.Flow {
	side := func() *artifact.GrpcMessage {
		return &artifact.GrpcMessage{Kind: "grpc", Procedure: "/stock.v1.Inventory/Watch", StreamKind: "bidirectional", ObservedBytes: 10,
			Observation: &artifact.GrpcObservation{CompleteFrames: 2, RetainedFrames: 2, ObservedBytes: 10, CountsComplete: true, EOF: true, End: "eof"},
			Metadata:    artifact.Headers{"content-type": {"application/grpc"}, "traceparent": {"trace"}},
		}
	}
	payload := func(index int, sequence uint64, value string) artifact.GrpcPayload {
		return artifact.GrpcPayload{Index: index, Type: "stock.v1.Item", DecodeState: "decoded", Body: &artifact.Body{Encoding: "json", Text: value},
			Observation: &artifact.GrpcPayloadObservation{StartSequence: sequence, CompleteSequence: sequence + 1, StartedOffsetNS: int64(sequence), CompletedOffsetNS: int64(sequence + 1)},
		}
	}
	req, res := side(), side()
	req.Messages = []artifact.GrpcPayload{payload(0, 1, `{"id":"one"}`), payload(1, 5, `{"id":"two"}`)}
	res.Messages = []artifact.GrpcPayload{payload(0, 3, `{"available":true}`), payload(1, 7, `{"available":false}`)}
	res.HTTPStatus, res.StatusSource = 200, "trailers"
	return artifact.Flow{ID: "rpc", Protocol: "grpc", Request: artifact.Message{GRPC: req}, Response: artifact.Message{GRPC: res}}
}

func TestNormalizeGRPCPreservesMessagesStatusAndInterleaving(t *testing.T) {
	base := grpcNormalizationFixture()
	baseReq, baseRes := normalizedSignatures(t, base)
	for _, tc := range []struct {
		name           string
		edit           func(*artifact.Flow)
		requestChanges bool
	}{
		{"procedure", func(f *artifact.Flow) {
			f.Request.GRPC.Procedure, f.Response.GRPC.Procedure = "/stock.v1.Inventory/Other", "/stock.v1.Inventory/Other"
		}, true},
		{"request value", func(f *artifact.Flow) { f.Request.GRPC.Messages[0].Body.Text = `{"id":"other"}` }, true},
		{"request metadata", func(f *artifact.Flow) { f.Request.GRPC.Metadata["authorization"] = []string{"Bearer dummy"} }, true},
		{"message type", func(f *artifact.Flow) { f.Request.GRPC.Messages[0].Type = "stock.v1.Other" }, true},
		{"response value", func(f *artifact.Flow) { f.Response.GRPC.Messages[0].Body.Text = `{"available":false}` }, false},
		{"application error code", func(f *artifact.Flow) { f.Response.GRPC.StatusCode = 5 }, false},
		{"application error message", func(f *artifact.Flow) { f.Response.GRPC.StatusMessage = "not found" }, false},
		{"response metadata", func(f *artifact.Flow) { f.Response.GRPC.Metadata["x-result"] = []string{"other"} }, false},
		{"response trailers", func(f *artifact.Flow) { f.Response.GRPC.Trailers = artifact.Headers{"x-result": {"other"}} }, false},
		{"message interleaving", func(f *artifact.Flow) {
			f.Request.GRPC.Messages[1].Observation.StartSequence, f.Request.GRPC.Messages[1].Observation.CompleteSequence = 3, 4
			f.Request.GRPC.Messages[1].Observation.StartedOffsetNS, f.Request.GRPC.Messages[1].Observation.CompletedOffsetNS = 3, 4
			f.Response.GRPC.Messages[0].Observation.StartSequence, f.Response.GRPC.Messages[0].Observation.CompleteSequence = 5, 6
			f.Response.GRPC.Messages[0].Observation.StartedOffsetNS, f.Response.GRPC.Messages[0].Observation.CompletedOffsetNS = 5, 6
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := cloneNormalizationFlow(t, base)
			tc.edit(&other)
			request, response := normalizedSignatures(t, other)
			if tc.requestChanges && request == baseReq || !tc.requestChanges && (request != baseReq || response == baseRes) {
				t.Fatal("gRPC application semantics were lost or changed scenario identity")
			}
		})
	}
	equivalent := cloneNormalizationFlow(t, base)
	for _, side := range []*artifact.GrpcMessage{equivalent.Request.GRPC, equivalent.Response.GRPC} {
		side.Metadata["traceparent"] = []string{"another-trace"}
		for i := range side.Messages {
			side.Messages[i].Observation.StartedOffsetNS += 100
			side.Messages[i].Observation.CompletedOffsetNS += 100
			side.Messages[i].Observation.StartSequence += 100
			side.Messages[i].Observation.CompleteSequence += 100
		}
	}
	req, res := normalizedSignatures(t, equivalent)
	if req != baseReq || res != baseRes {
		t.Fatal("gRPC timestamps, absolute sequence IDs or trace metadata affected comparison")
	}
}

func TestNormalizeGRPCRejectsIncompleteOrUndecodedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*artifact.Flow)
	}{
		{"legacy missing observations", func(f *artifact.Flow) { f.Request.GRPC.Observation = nil; f.Response.GRPC.Observation = nil }},
		{"direction open", func(f *artifact.Flow) {
			f.Response.GRPC.Observation.CountsComplete = false
			f.Response.GRPC.Observation.EOF = false
			f.Response.GRPC.Observation.End = "open"
		}},
		{"direction truncated", func(f *artifact.Flow) { f.Response.GRPC.CaptureTruncated = true }},
		{"undecoded message", func(f *artifact.Flow) {
			f.Response.GRPC.Messages[0].DecodeState = "unavailable"
			f.Response.GRPC.Messages[0].DecodeError = "unknown type"
			f.Response.GRPC.Messages[0].Body = nil
		}},
		{"message redacted", func(f *artifact.Flow) { f.Response.GRPC.Messages[0].Redacted = true }},
		{"message body truncated", func(f *artifact.Flow) { f.Response.GRPC.Messages[0].Body.Truncated = true }},
		{"terminal status missing", func(f *artifact.Flow) { f.Response.GRPC.StatusSource = "" }},
		{"terminal status invalid", func(f *artifact.Flow) { f.Response.GRPC.StatusCode = 17 }},
		{"stream kind unknown", func(f *artifact.Flow) { f.Request.GRPC.StreamKind, f.Response.GRPC.StreamKind = "unknown", "unknown" }},
		{"rich status truncated", func(f *artifact.Flow) {
			f.Response.GRPC.StatusDetails = &artifact.GrpcStatusDetails{State: "truncated"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flow := grpcNormalizationFixture()
			tc.edit(&flow)
			if _, _, err := normalizeFlow(flow, nil); err == nil {
				t.Fatal("incomplete gRPC evidence was accepted")
			}
		})
	}
}

func TestNormalizeGRPCRichErrorsPreserveMeaningAndExcludeProvenance(t *testing.T) {
	flow := grpcNormalizationFixture()
	code := 5
	flow.Response.GRPC.StatusCode = code
	flow.Response.GRPC.StatusDetails = &artifact.GrpcStatusDetails{State: "decoded", Source: "trailers", Code: &code, Message: "missing", Wire: &artifact.Body{Bytes: "CAU="}, AuxiliarySHA256: "source-hash",
		Details: []artifact.GrpcStatusDetail{{Index: 0, TypeURL: "type.googleapis.com/example.Error", Type: "example.Error", State: "decoded", DescriptorSHA256: "first-schema", Wire: &artifact.Body{Bytes: "CAE="}, Body: &artifact.Body{Encoding: "json", Text: `{"reason":"missing"}`}}},
	}
	baseReq, baseRes := normalizedSignatures(t, flow)
	other := cloneNormalizationFlow(t, flow)
	other.Response.GRPC.StatusDetails.AuxiliarySHA256 = "other-hash"
	other.Response.GRPC.StatusDetails.Details[0].DescriptorSHA256 = "other-schema"
	req, res := normalizedSignatures(t, other)
	if req != baseReq || res != baseRes {
		t.Fatal("local descriptor provenance changed rich error semantics")
	}
	other.Response.GRPC.StatusDetails.Details[0].Body.Text = `{"reason":"permission_denied"}`
	_, res = normalizedSignatures(t, other)
	if res == baseRes {
		t.Fatal("changed rich error detail was lost")
	}
	other.Response.GRPC.StatusDetails.Details[0].Wire.Truncated = true
	if _, _, err := normalizeFlow(other, nil); err == nil {
		t.Fatal("partial rich error detail was accepted")
	}
}

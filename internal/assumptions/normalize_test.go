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

func TestNormalizeHTTPOnlyNormalizesDeclaredJSONBodies(t *testing.T) {
	for _, tc := range []struct {
		media string
		equal bool
	}{
		{"application/json", true},
		{"application/problem+json; charset=utf-8", true},
		{"Application/JSON", true},
		{"", true}, // Legacy captures can lack Content-Type.
		{"text/plain", false},
		{"application/octet-stream", false},
		{"application/x-www-form-urlencoded", false},
		{"application/json; invalid", false},
	} {
		t.Run(tc.media, func(t *testing.T) {
			left, right := httpNormalizationFixture(), httpNormalizationFixture()
			for _, message := range []*artifact.HttpMessage{left.Request.HTTP, right.Request.HTTP, left.Response.HTTP, right.Response.HTTP} {
				delete(message.Headers, "Content-Type")
				if tc.media != "" {
					message.Headers["Content-Type"] = []string{tc.media}
				}
			}
			left.Request.HTTP.Body.Text, right.Request.HTTP.Body.Text = "1", "1.0"
			left.Response.HTTP.Body.Text, right.Response.HTTP.Body.Text = "[1,2]", "[1, 2]"
			lreq, lres := normalizedSignatures(t, left)
			rreq, rres := normalizedSignatures(t, right)
			if (lreq == rreq) != tc.equal || (lres == rres) != tc.equal {
				t.Fatalf("media %q lost its body comparison semantics: request equal %t, response equal %t", tc.media, lreq == rreq, lres == rres)
			}
		})
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

package redact

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/phrond/wiretap/internal/artifact"
)

func TestHTTPBodyRedactionOmitsUnsafeRepresentations(t *testing.T) {
	const secret = "redaction-secret-marker"
	for _, body := range []*artifact.Body{
		{Text: `{"token":"` + secret + `"`, Truncated: true},
		{Text: `{"token":"` + secret + `"`},
		{Text: `{"token":"` + secret + `"} trailing`},
		{Text: secret},
		{Encoding: "base64", Bytes: base64.StdEncoding.EncodeToString([]byte(secret))},
		{Text: `{"token":"` + secret + `"}`, Decoded: &artifact.DecodedBody{Text: secret}},
	} {
		a := &artifact.Artifact{Traffic: []artifact.Flow{{Request: artifact.Message{HTTP: &artifact.HttpMessage{Body: body}}}}}
		if err := Apply(a, []artifact.Redaction{{Field: "request.body.token", Rule: "remove"}}); err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), secret) || strings.Contains(string(data), base64.StdEncoding.EncodeToString([]byte(secret))) {
			t.Fatalf("unsafe representation retained: %s", data)
		}
		if body.Text == "" && !body.Omitted {
			t.Fatal("omitted body lost unavailable marker")
		}
	}
}

func TestHTTPURLRulesAndDiagnosticCopies(t *testing.T) {
	const secret = "query-secret-marker"
	issues := []artifact.SchemaValidationError{{FlowID: "flow", Phase: "request", Message: secret}}
	a := &artifact.Artifact{Env: &artifact.Environment{SUTBaseURL: "http://host?token=" + secret}, SchemaValidation: &artifact.SchemaValidation{Status: "fail", Errors: issues}, Meta: map[string]any{"schema_validation": map[string]any{"errors": issues}}, Assertions: []artifact.Assertion{{FlowID: "flow", Phase: "request", Message: secret, Actual: secret}}, Traffic: []artifact.Flow{{ID: "flow", TransportError: secret, Request: artifact.Message{HTTP: &artifact.HttpMessage{URL: "http://user:password@host/path?token=" + secret + "&token=" + secret + "&keep=yes#" + secret}}}}}
	rules := []artifact.Redaction{{Field: "request.query.token", Rule: "remove"}, {Field: "request.url.userinfo", Rule: "remove"}, {Field: "request.url.fragment", Rule: "remove"}}
	if err := Apply(a, rules); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(a)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "password") {
		t.Fatalf("secret copy retained: %s", raw)
	}
	u, err := url.Parse(a.Traffic[0].Request.HTTP.URL)
	if err != nil || u.Query().Get("keep") != "yes" || u.Query().Has("token") {
		t.Fatalf("unrelated query changed: %v %v", u, err)
	}
}

func TestMalformedQueryFailsClosed(t *testing.T) {
	msg := &artifact.HttpMessage{URL: "http://host/path?token=secret%ZZ&keep=yes"}
	if err := Apply(&artifact.Artifact{Traffic: []artifact.Flow{{Request: artifact.Message{HTTP: msg}}}}, []artifact.Redaction{{Field: "request.url.query.token", Rule: "remove"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(msg.URL, "secret") {
		t.Fatal("malformed query leaked")
	}
}

func TestQueryMaskPreservesRepeatedValuesAndPublicParameters(t *testing.T) {
	msg := &artifact.HttpMessage{URL: "http://host/items?token=first&keep=yes&token=second"}
	if err := Apply(&artifact.Artifact{Traffic: []artifact.Flow{{Request: artifact.Message{HTTP: msg}}}}, []artifact.Redaction{{Field: "request.url.query.token", Rule: "mask", Replacement: "removed"}}); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(msg.URL)
	if err != nil || len(u.Query()["token"]) != 2 || u.Query()["token"][0] != "removed" || u.Query()["token"][1] != "removed" || u.Query().Get("keep") != "yes" {
		t.Fatalf("incorrect masked query: %s %v", msg.URL, err)
	}
}

func TestInvalidRedactionSelectorsAreRejected(t *testing.T) {
	for _, field := range []string{"request.messages[invalid].body.token", "request.messages[-1].body.token", "request.url.query.", "request.password.token"} {
		if err := Validate([]artifact.Redaction{{Field: field, Rule: "remove"}}); err == nil {
			t.Fatalf("invalid policy accepted: %s", field)
		}
	}
}

func TestWholeGRPCMessageBodyRedactionKeepsArchiveEncoding(t *testing.T) {
	for _, field := range []string{"request.body", "request.messages[*].body"} {
		a := &artifact.Artifact{Traffic: []artifact.Flow{{Request: artifact.Message{GRPC: &artifact.GrpcMessage{Messages: []artifact.GrpcPayload{{Body: &artifact.Body{Encoding: "json", Text: `{"token":"secret"}`}}}}}}}}
		if err := Apply(a, []artifact.Redaction{{Field: field, Rule: "remove"}}); err != nil {
			t.Fatal(err)
		}
		body := a.Traffic[0].Request.GRPC.Messages[0].Body
		if body != nil && (body.Encoding != "json" || strings.Contains(body.Text, "secret")) {
			t.Fatalf("unsafe gRPC body: %+v", body)
		}
	}
}

func TestDuplicateAncestorJSONKeysFailClosed(t *testing.T) {
	for _, body := range []string{`{"credentials":{"token":"secret-marker"},"credentials":{}}`, `{"credentials":{},"credentials":{"token":"secret-marker"}}`, `{"credentials":{"token":"secret-marker","token":"public"}}`} {
		msg := &artifact.HttpMessage{Body: &artifact.Body{Text: body}}
		if err := Apply(&artifact.Artifact{Traffic: []artifact.Flow{{Request: artifact.Message{HTTP: msg}}}}, []artifact.Redaction{{Field: "request.body.credentials.token", Rule: "remove"}}); err != nil {
			t.Fatal(err)
		}
		if msg.Body.Text != "" || !msg.Body.Omitted {
			t.Fatalf("ambiguous JSON retained: %+v", msg.Body)
		}
	}
}

func TestOpaqueURLIsOmittedByURLPolicy(t *testing.T) {
	msg := &artifact.HttpMessage{URL: "http:opaque-secret-marker"}
	if err := Apply(&artifact.Artifact{Traffic: []artifact.Flow{{Request: artifact.Message{HTTP: msg}}}}, []artifact.Redaction{{Field: "request.url.path", Rule: "remove"}}); err != nil {
		t.Fatal(err)
	}
	if msg.URL != "" {
		t.Fatalf("opaque URL leaked: %s", msg.URL)
	}
}

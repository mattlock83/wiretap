package tracecontext

import (
	"net/http"
	"testing"
)

func TestValidationAndInjection(t *testing.T) {
	valid := "00-11111111111111111111111111111111-2222222222222222-00"
	sc, err := (Context{Traceparent: valid, Tracestate: "vendor=value"}).Validate()
	if err != nil || !sc.IsValid() || sc.IsSampled() {
		t.Fatalf("unsampled context: %v %v", sc, err)
	}
	headers := http.Header{"Traceparent": []string{"invalid"}, "Tracestate": []string{"stale=value"}}
	Inject(headers, sc)
	if headers.Get("Traceparent") != valid || headers.Get("Tracestate") != "vendor=value" {
		t.Fatalf("context not replaced: %v", headers)
	}
	sc, _ = (Context{Traceparent: valid}).Validate()
	Inject(headers, sc)
	if headers.Get("Tracestate") != "" {
		t.Fatal("orphan tracestate retained")
	}
	headers.Add("traceparent", valid)
	if Extract(headers).IsValid() {
		t.Fatal("duplicate parents accepted")
	}
	for _, c := range []Context{{Traceparent: "bad"}, {Traceparent: "00-00000000000000000000000000000000-2222222222222222-01"}, {Traceparent: valid, Tracestate: "bad=value,bad=duplicate"}} {
		if _, err := c.Validate(); err == nil {
			t.Fatalf("accepted invalid registration %#v", c)
		}
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/version"
)

func TestVersionCommandReportsProducerWithoutRuntimeConfiguration(t *testing.T) {
	t.Setenv("WIRETAP_HTTP_LISTEN", "invalid address")
	for _, args := range [][]string{{"version"}, {"--version"}, {"version", "--json"}, {"--version", "--json"}} {
		if code := execute(context.Background(), args); code != 0 {
			t.Fatalf("execute(%v) returned %d", args, code)
		}
	}
	var data bytes.Buffer
	if err := versionCommand([]string{"--json"}, &data); err != nil {
		t.Fatal(err)
	}
	var producer version.Producer
	if err := json.Unmarshal(data.Bytes(), &producer); err != nil || producer != version.Current() {
		t.Fatalf("incorrect producer: %+v %v", producer, err)
	}
	data.Reset()
	if err := versionCommand(nil, &data); err != nil || !strings.HasPrefix(data.String(), "wiretap "+producer.Version) {
		t.Fatalf("incorrect human version: %s %v", data.String(), err)
	}
	if err := versionCommand([]string{"unexpected"}, &data); err == nil {
		t.Fatal("accepted positional argument")
	}
}

func TestVersionCommandRetainsInjectedDirtyProvenance(t *testing.T) {
	previous := version.BuildDirty
	version.BuildDirty = "true"
	t.Cleanup(func() { version.BuildDirty = previous })
	var data bytes.Buffer
	if err := versionCommand([]string{"--json"}, &data); err != nil {
		t.Fatal(err)
	}
	var producer version.Producer
	if err := json.Unmarshal(data.Bytes(), &producer); err != nil || !producer.Dirty {
		t.Fatalf("dirty local source was labelled clean: %+v %v", producer, err)
	}
}

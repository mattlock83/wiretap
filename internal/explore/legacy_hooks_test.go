package explore

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestHookHelper(t *testing.T) {
	if os.Getenv("WIRETAP_HOOK_HELPER") != "1" {
		return
	}
	args := os.Args
	mode := args[len(args)-1]
	switch mode {
	case "echo":
		data, _ := io.ReadAll(os.Stdin)
		fmt.Fprint(os.Stdout, string(data))
	case "empty":
	case "fail":
		os.Exit(3)
	case "sleep":
		time.Sleep(time.Minute)
	case "large":
		fmt.Fprint(os.Stdout, strings.Repeat("x", 2*1024*1024))
	default:
		fmt.Fprint(os.Stdout, mode)
	}
	os.Exit(0)
}

func TestCommandProtocol(t *testing.T) {
	t.Setenv("WIRETAP_HOOK_HELPER", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	for _, tc := range []struct {
		mode    string
		wantErr bool
	}{
		{"echo", false}, {"empty", false}, {"fail", true}, {"large", true},
		{`{"bindings":{"n":1234567890123456789}}`, false},
		{`{"unknown":1}`, true}, {`{} {}`, true}, {`null`, true},
		{`{"checks":[{"name":"","passed":true}]}`, true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			cmd := &legacyHookCommand{Command: []string{os.Args[0], "-test.run=TestHookHelper", "--", tc.mode}}
			out, err := callLegacyHook(context.Background(), cmd, t.TempDir(), map[string]any{"bindings": map[string]any{"hello": "world"}}, io.Discard)
			if (err != nil) != tc.wantErr {
				t.Fatalf("output %+v error %v", out, err)
			}
			if tc.mode == "echo" && out.Bindings["hello"] != "world" {
				t.Fatal("stdin was not delivered")
			}
		})
	}
}

func TestHookTimeoutAndCancellation(t *testing.T) {
	t.Setenv("WIRETAP_HOOK_HELPER", "1")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	cmd := &legacyHookCommand{Command: []string{os.Args[0], "-test.run=TestHookHelper", "--", "sleep"}, Timeout: "50ms"}
	start := time.Now()
	_, err := callLegacyHook(context.Background(), cmd, "", nil, io.Discard)
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("timeout failed: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = callLegacyHook(ctx, cmd, "", nil, io.Discard); err == nil {
		t.Fatal("cancelled context executed hook")
	}
}

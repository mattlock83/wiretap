package basicgrpc

import (
	"encoding/base64"
	"testing"

	"github.com/phrond/evermock/examples/basic-grpc/examplepb"
	"google.golang.org/protobuf/proto"
)

func TestBasicGRPC(t *testing.T) {
	h := newHarness(t)

	sutListen := getenv("SUT_LISTEN", "127.0.0.1:18095")
	grpcProxyListen := getenv("PHROND_GRPC_PROXY_LISTEN", "127.0.0.1:18082")
	grpcMockListen := getenv("PHROND_GRPC_MOCK_LISTEN", "127.0.0.1:18083")

	_, err := h.postJSON("/v1/testcases/start", map[string]any{
		"name":    "basic-grpc",
		"meta":    map[string]any{"suite": "basic-grpc"},
		"schemas": grpcSchemas(),
	})
	if err != nil {
		t.Fatalf("start testcase: %v", err)
	}

	defer func() {
		if t.Failed() {
			_, _ = h.postJSON("/v1/testcases/finish", map[string]any{"status": "fail"})
		}
	}()

	sutCmd, err := startSUT(sutListen, grpcMockListen)
	if err != nil {
		t.Fatalf("start SUT: %v", err)
	}
	defer killProcess(sutCmd)

	if err := waitForPort(sutListen, 40); err != nil {
		t.Fatalf("wait for SUT: %v", err)
	}

	alphaPayload := examplepb.NewDependencyReply("alpha-ok")
	alphaBytes, err := proto.Marshal(alphaPayload)
	if err != nil {
		t.Fatalf("marshal alpha reply: %v", err)
	}

	betaPayload := examplepb.NewDependencyReply("beta-ok")
	betaBytes, err := proto.Marshal(betaPayload)
	if err != nil {
		t.Fatalf("marshal beta reply: %v", err)
	}

	_, err = h.postJSON("/v1/grpc/stubs", map[string]any{
		"id":   "alpha",
		"path": examplepb.DependencyAlphaMethod,
		"response": map[string]any{
			"status":      0,
			"body_base64": base64.StdEncoding.EncodeToString(alphaBytes),
		},
	})
	if err != nil {
		t.Fatalf("register alpha stub: %v", err)
	}

	_, err = h.postJSON("/v1/grpc/stubs", map[string]any{
		"id":   "beta",
		"path": examplepb.DependencyBetaMethod,
		"response": map[string]any{
			"status":      0,
			"body_base64": base64.StdEncoding.EncodeToString(betaBytes),
		},
	})
	if err != nil {
		t.Fatalf("register beta stub: %v", err)
	}

	result, err := callProcess(grpcProxyListen, "123", "process")
	if err != nil {
		t.Fatalf("call process: %v", err)
	}
	if result != "alpha:alpha-ok|beta:beta-ok" {
		t.Fatalf("unexpected grpc result: %q", result)
	}

	finishResp, err := h.postJSON("/v1/testcases/finish", map[string]any{"status": "pass"})
	if err != nil {
		t.Fatalf("finish testcase: %v", err)
	}
	assertSchemaPass(t, finishResp)
}

package consumerprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	wiretap "github.com/mattlock83/wiretap/sdk/go"
)

// Each child is an ordinary SDK-managed test suite with its own capture. The
// outer workflow runs those suites and checks the resulting immutable evidence.
func TestMain(m *testing.M) {
	phase := os.Getenv("WIRETAP_EXAMPLE_CAPTURE")
	if phase == "" {
		os.Exit(m.Run())
	}
	options := wiretap.RunOptions{OutputDir: os.Getenv("WIRETAP_EXAMPLE_OUTPUT")}
	var closeMock func()
	if phase == "consumer" {
		mock := httptest.NewServer(inventoryHandler(false))
		closeMock = mock.Close
		_ = os.Setenv("INVENTORY_MOCK_URL", mock.URL)
		options.Config = "consumer.yaml"
	} else {
		options.Config = "provider.yaml"
		options.Handler = inventoryHandler(phase == "breaking")
		schema := "inventory.openapi.yaml"
		if phase == "breaking" {
			schema = "inventory-renamed.openapi.yaml"
		}
		_ = os.Setenv("INVENTORY_SCHEMA", schema)
	}
	code := wiretap.Run(m, options)
	if closeMock != nil {
		closeMock()
	}
	os.Exit(code)
}

func TestOrderUsesInventory(t *testing.T) {
	if os.Getenv("WIRETAP_EXAMPLE_CAPTURE") != "consumer" {
		t.Skip("run by TestConsumerProvider with its own capture")
	}
	// Reserve the application's URL, then give it the captured dependency URL
	// before accepting requests. Tests and application use ordinary HTTP clients.
	orders := httptest.NewUnstartedServer(nil)
	t.Cleanup(orders.Close)
	session := wiretap.StartTest(t, wiretap.TestOptions{
		SUTBaseURL: "http://" + orders.Listener.Addr().String(),
	})
	orders.Config.Handler = ordersHandler(session.Dependencies["inventory"])
	orders.Start()
	response, err := session.HTTPClient(nil).Get(session.SUTURL + "/orders/order-1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var order struct {
		Ready bool `json:"ready"`
	}
	if err := json.NewDecoder(response.Body).Decode(&order); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || !order.Ready {
		t.Fatalf("order was not ready: status=%d, body=%+v", response.StatusCode, order)
	}
}

func TestInventoryQuantity(t *testing.T) {
	phase := os.Getenv("WIRETAP_EXAMPLE_CAPTURE")
	if phase == "" || phase == "consumer" {
		t.Skip("run by TestConsumerProvider with its own capture")
	}
	session := wiretap.StartTest(t)
	response, err := session.HTTPClient(nil).Get(session.SUTURL + "/stock/widget-1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var stock map[string]int
	if err := json.NewDecoder(response.Body).Decode(&stock); err != nil {
		t.Fatal(err)
	}
	field := "quantity"
	if phase == "breaking" {
		field = "availableQuantity"
	}
	if response.StatusCode != 200 || len(stock) != 1 || stock[field] != 3 {
		t.Fatalf("unexpected inventory: status=%d, body=%v", response.StatusCode, stock)
	}
}

func TestConsumerProvider(t *testing.T) {
	binary := os.Getenv("WIRETAP_BINARY")
	if binary == "" {
		t.Skip("set WIRETAP_BINARY to run the complete capture/check workflow; see README.md")
	}
	binary, err := exec.LookPath(binary)
	if err != nil {
		t.Fatal(err)
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("WIRETAP_BINARY", binary)
	output := os.Getenv("WIRETAP_OUTPUT_DIR")
	if output == "" {
		output = "artifacts"
	}
	output, err = filepath.Abs(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(output, 0o755); err != nil {
		t.Fatal(err)
	}
	output, err = os.MkdirTemp(output, "workflow-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Workflow evidence (retained on success and failure): %s", output)
	consumer := capture(t, output, "consumer", "TestOrderUsesInventory")
	for _, phase := range []string{"original", "breaking", "fixed"} {
		t.Run(phase, func(t *testing.T) {
			provider := capture(t, output, phase, "TestInventoryQuantity")
			for _, viewpoint := range []string{"consumer", "provider"} {
				for _, inputFormat := range []string{"postman", "arazzo"} {
					check(t, binary, output, phase, viewpoint, consumer, provider, phase != "breaking", inputFormat)
				}
			}
		})
	}
}

func capture(t *testing.T, output, phase, testName string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, executable, "-test.run=^"+testName+"$", "-test.v")
	phaseOutput := filepath.Join(output, phase)
	command.Env = append(os.Environ(), "WIRETAP_EXAMPLE_CAPTURE="+phase,
		"WIRETAP_EXAMPLE_OUTPUT="+phaseOutput, "WIRETAP_OUTPUT_DIR="+phaseOutput)
	log, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s capture: %v\n%s", phase, err, log)
	}
	// Each phase has a fresh, private output directory. Require its single
	// archive rather than guessing which capture is newest in a shared folder.
	entries, err := os.ReadDir(phaseOutput)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !entries[0].IsDir() {
		t.Fatalf("expected exactly one %s archive in %s", phase, phaseOutput)
	}
	bundlePath := filepath.Join(phaseOutput, entries[0].Name())
	receiptPath := filepath.Join(bundlePath, "last-run.json")
	var receipt wiretap.FinishResult
	readJSON(t, receiptPath, &receipt)
	if receipt.Status != "complete" || receipt.BundlePath != bundlePath || receipt.Outputs == nil || receipt.Outputs.Coverage["json"] == "" || len(receipt.Outputs.Postman) == 0 || receipt.Outputs.Arazzo == "" {
		t.Fatalf("%s did not produce a complete receipt: %+v\n%s", phase, receipt, log)
	}
	t.Logf("%s tests passed; receipt: %s", phase, receiptPath)
	t.Logf("coverage: %s; Postman: %v; Arazzo: %s", receipt.Outputs.Coverage["json"], receipt.Outputs.Postman, receipt.Outputs.Arazzo)
	return receipt.BundlePath
}

func check(t *testing.T, binary, output, phase, viewpoint, consumer, provider string, compatible bool, inputFormat string) {
	t.Helper()
	stem := phase + "-" + viewpoint
	if inputFormat == "arazzo" {
		stem += "-arazzo"
	}
	reportPath := filepath.Join(output, stem+"-check.json")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "check", viewpoint,
		"--consumer", consumer, "--provider", provider, "--dependency", "inventory",
		"--input-format", inputFormat, "--format", "json", "--output", reportPath)
	log, err := command.CombinedOutput()
	exit := 0
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		exit = exitError.ExitCode()
	} else if err != nil {
		t.Fatalf("run check: %v\n%s", err, log)
	}
	wantExit := 0
	if !compatible {
		wantExit = 1
	}
	if exit != wantExit {
		t.Fatalf("%s %s check exit=%d, want %d\n%s", phase, viewpoint, exit, wantExit, log)
	}
	var report struct {
		Compatible bool `json:"compatible"`
		Counts     struct{ Assumptions, Verified, Mismatched int }
		Matches    []struct {
			ContractCheck string `json:"contract_check"`
		}
		Findings []struct{ Code string }
	}
	readJSON(t, reportPath, &report)
	if report.Compatible != compatible || report.Counts.Assumptions != 1 {
		t.Fatalf("unexpected %s report: %+v", phase, report)
	}
	if compatible {
		if report.Counts.Verified != 1 || len(report.Matches) != 1 || report.Matches[0].ContractCheck != "openapi_3_0_provider_contract" {
			t.Fatalf("expected a provider-contract-backed match: %+v", report)
		}
	} else if report.Counts.Mismatched != 1 || len(report.Findings) != 1 || report.Findings[0].Code != "provider_response_mismatch" {
		t.Fatalf("expected the renamed response field to mismatch: %+v", report)
	}
	t.Logf("%s %s %s check: compatible=%t, exit=%d; %s", phase, viewpoint, inputFormat, report.Compatible, exit, reportPath)
}

func readJSON(t *testing.T, path string, target any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
}

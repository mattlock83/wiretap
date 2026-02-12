package coverage

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phrond/evermock/internal/artifact"
)

func TestPostmanCollectionRoundTrip_RebuildsCoverage(t *testing.T) {
	report := sampleCoverageReport(t)

	collection, err := CollectionFromReport(report)
	if err != nil {
		t.Fatalf("CollectionFromReport() error = %v", err)
	}
	rebuilt, err := ReportFromPostmanCollection(collection)
	if err != nil {
		t.Fatalf("ReportFromPostmanCollection() error = %v", err)
	}

	if rebuilt.SUTName != report.SUTName {
		t.Fatalf("sut name mismatch: want %q got %q", report.SUTName, rebuilt.SUTName)
	}

	if len(rebuilt.Provided.Specs) != 1 {
		t.Fatalf("expected one provided spec, got %d", len(rebuilt.Provided.Specs))
	}
	provided := rebuilt.Provided.Specs[0]
	if provided.SchemaID != "payment-openapi" {
		t.Fatalf("unexpected provided schema id: %q", provided.SchemaID)
	}
	if !hasOperation(provided.Exercised, "POST /payments/submit") {
		t.Fatalf("expected exercised operation POST /payments/submit")
	}
	if !hasOperation(provided.NotCovered, "GET /healthz") {
		t.Fatalf("expected not covered operation GET /healthz")
	}

	if len(rebuilt.Dependent.Specs) != 1 {
		t.Fatalf("expected one dependent spec, got %d", len(rebuilt.Dependent.Specs))
	}
	dependent := rebuilt.Dependent.Specs[0]
	if dependent.SchemaID != "account-openapi" {
		t.Fatalf("unexpected dependent schema id: %q", dependent.SchemaID)
	}
	if !hasOperation(dependent.Faked, "GET /accounts/{account_id}") {
		t.Fatalf("expected faked operation GET /accounts/{account_id}")
	}
}

func TestWriteReportHTML_UsesPostmanRoundTrip(t *testing.T) {
	report := sampleCoverageReport(t)

	var out bytes.Buffer
	if err := WriteReport(&out, report, "html"); err != nil {
		t.Fatalf("WriteReport(html) error = %v", err)
	}
	html := out.String()
	if !strings.Contains(html, "Phrond Coverage Report") {
		t.Fatalf("expected report title in html output")
	}
	if !strings.Contains(html, "POST /payments/submit") {
		t.Fatalf("expected exercised operation in html output")
	}
	if !strings.Contains(html, "GET /healthz") {
		t.Fatalf("expected not covered operation in html output")
	}
	if !strings.Contains(html, "GET /accounts/{account_id}") {
		t.Fatalf("expected dependent operation in html output")
	}
}

func TestCollectionFromReport_UsesSourceFolderNamesAndVariables(t *testing.T) {
	report := sampleCoverageReport(t)

	collection, err := CollectionFromReport(report)
	if err != nil {
		t.Fatalf("CollectionFromReport() error = %v", err)
	}
	if len(collection.Variable) == 0 {
		t.Fatalf("expected spec variables in collection output")
	}

	var (
		foundSpecFolder bool
		foundVarURL     bool
		foundPathField  bool
		foundVarKey     bool
	)
	expectedProvidedFolder := filepath.Base(report.Provided.Specs[0].Source) + " (payment)"
	expectedProvidedVarKey := "payment_openapi_yaml_payment_base_url"
	for _, testFolder := range collection.Item {
		for _, trafficFolder := range testFolder.Item {
			for _, specFolder := range trafficFolder.Item {
				if specFolder.Name == expectedProvidedFolder {
					foundSpecFolder = true
				}
				for _, sample := range specFolder.Item {
					if sample.Request == nil {
						continue
					}
					if sample.Request.URL.Raw == "{{"+expectedProvidedVarKey+"}}/payments/submit" {
						foundVarURL = true
					}
					if len(sample.Request.URL.Path) > 0 && sample.Request.URL.Path[0] == "payments" {
						foundPathField = true
					}
				}
			}
		}
	}
	for _, variable := range collection.Variable {
		if variable.Key == expectedProvidedVarKey {
			foundVarKey = true
			break
		}
	}
	if !foundSpecFolder {
		t.Fatalf("expected spec folder named by schema file and title")
	}
	if !foundVarKey {
		t.Fatalf("expected per-spec variable key derived from schema file and title")
	}
	if !foundVarURL {
		t.Fatalf("expected request URL to use renamed per-spec variable and path")
	}
	if !foundPathField {
		t.Fatalf("expected structured request.url.path fields to be populated")
	}
}

func TestCollectionFromReport_OmitsEmptyMockTrafficFolder(t *testing.T) {
	report := sampleCoverageReport(t)
	report.Dependent.Specs = nil
	report.TestCases[0].Dependent = nil

	collection, err := CollectionFromReport(report)
	if err != nil {
		t.Fatalf("CollectionFromReport() error = %v", err)
	}
	if len(collection.Item) != 1 {
		t.Fatalf("expected one testcase folder, got %d", len(collection.Item))
	}
	folders := collection.Item[0].Item
	if len(folders) != 1 {
		t.Fatalf("expected only one traffic folder when no mock samples, got %d", len(folders))
	}
	if folders[0].Name != "test traffic" {
		t.Fatalf("unexpected folder name %q", folders[0].Name)
	}
}

func sampleCoverageReport(t *testing.T) *Report {
	t.Helper()

	tmp := t.TempDir()
	paymentSpec := filepath.Join(tmp, "payment-openapi.yaml")
	accountSpec := filepath.Join(tmp, "account-openapi.yaml")

	writeTestFile(t, paymentSpec, `openapi: 3.0.0
info:
  title: payment
  version: "1"
paths:
  /payments/submit:
    post:
      responses:
        "201":
          description: created
  /healthz:
    get:
      responses:
        "200":
          description: ok
`)
	writeTestFile(t, accountSpec, `openapi: 3.0.0
info:
  title: account
  version: "1"
paths:
  /accounts/{account_id}:
    get:
      parameters:
        - in: path
          name: account_id
          required: true
          schema:
            type: string
      responses:
        "200":
          description: ok
`)

	return &Report{
		GeneratedAt:   time.Date(2026, 2, 11, 12, 0, 0, 0, time.UTC),
		SUTName:       "Payments SUT",
		ArtifactCount: 1,
		Provided: SpecCoverageGroup{
			Specs: []SpecCoverage{
				{
					SchemaID: "payment-openapi",
					Role:     "sut",
					Type:     "openapi",
					Source:   paymentSpec,
				},
			},
		},
		Dependent: SpecCoverageGroup{
			Specs: []SpecCoverage{
				{
					SchemaID: "account-openapi",
					Role:     "fake",
					Type:     "openapi",
					Source:   accountSpec,
				},
			},
		},
		TestCases: []TestCaseCoverage{
			{
				Name: "submit payment",
				Provided: []SpecOperationSamples{
					{
						SchemaID: "payment-openapi",
						Type:     "openapi",
						Source:   paymentSpec,
						Operations: []OperationSamples{
							{
								Operation: "POST /payments/submit",
								Samples: []FlowSample{
									{
										TestCase: "submit payment",
										FlowID:   "flow-1",
										Protocol: "http",
										Channel:  "test_to_sut",
										Request: InteractionMessage{
											Method: "POST",
											URL:    "http://127.0.0.1:18110/payments/submit",
											Headers: artifact.Headers{
												"Content-Type": []string{"application/json"},
											},
											Body: `{"from_account":"acct-100","amount":120}`,
										},
										Response: InteractionMessage{
											Status: 201,
											Headers: artifact.Headers{
												"Content-Type": []string{"application/json"},
											},
											Body: `{"status":"submitted"}`,
										},
									},
								},
							},
						},
					},
				},
				Dependent: []SpecOperationSamples{
					{
						SchemaID: "account-openapi",
						Type:     "openapi",
						Source:   accountSpec,
						Operations: []OperationSamples{
							{
								Operation: "GET /accounts/{account_id}",
								Samples: []FlowSample{
									{
										TestCase: "submit payment",
										FlowID:   "flow-2",
										Protocol: "http",
										Channel:  "sut_to_mock",
										Request: InteractionMessage{
											Method: "GET",
											URL:    "/accounts/acct-100",
										},
										Response: InteractionMessage{
											Status: 200,
											Headers: artifact.Headers{
												"Content-Type": []string{"application/json"},
											},
											Body: `{"id":"acct-100","balance":1000}`,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}

func hasOperation(ops []OperationSamples, target string) bool {
	for _, op := range ops {
		if op.Operation == target {
			return true
		}
	}
	return false
}

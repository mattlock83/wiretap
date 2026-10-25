package wiretap

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestSharedInvalidReceiptConformance(t *testing.T) {
	data, err := os.ReadFile("../conformance/scenarios.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Version string `json:"contract_version"`
		Invalid map[string][]struct {
			ID  string          `json:"id"`
			Set json.RawMessage `json:"set"`
		} `json:"invalid_receipts"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Version != "1.0" {
		t.Fatalf("unsupported contract: %s", contract.Version)
	}
	for stage, vectors := range contract.Invalid {
		for _, vector := range vectors {
			t.Run(stage+"/"+vector.ID, func(t *testing.T) {
				var child *exec.Cmd
				mode := "conformance:" + stage + ":" + base64.StdEncoding.EncodeToString(vector.Set)
				owner, err := startManagedWithCommand(context.Background(), managedTestOptions(t), managedHelperCommand(mode, &child))
				if stage == "finish" {
					if err != nil {
						t.Fatal(err)
					}
					_, err = owner.close()
				} else if owner != nil {
					_, _ = owner.close()
					t.Fatal("invalid start receipt accepted")
				}
				if err == nil {
					t.Fatal("invalid receipt accepted")
				}
				want := map[string]string{"readiness": "owned local process", "start": "invalid run receipt", "finish": "mismatched run receipt"}[stage]
				if want == "" || !strings.Contains(err.Error(), want) {
					t.Fatalf("unexpected failure: %v", err)
				}
				if child.ProcessState == nil {
					t.Fatal("receipt failure leaked child")
				}
			})
		}
	}
}

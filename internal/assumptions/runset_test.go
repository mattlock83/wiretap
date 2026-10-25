package assumptions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mattlock83/wiretap/internal/bundle"
	"github.com/mattlock83/wiretap/internal/runset"
)

func writeRunSet(t *testing.T, paths ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "set.json")
	manifest, err := runset.Create(t.Context(), paths, path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func buildIdentity(identity bundle.Identity, revision string) bundle.Identity {
	if identity.SUTSource == nil {
		identity.SUTSource = map[string]any{}
	}
	identity.SUTSource["revision"] = revision
	return identity
}

func TestRunSetChecksShardsWithoutSplicingExchanges(t *testing.T) {
	first, second := httpNormalizationFixture(), httpNormalizationFixture()
	second.Request.HTTP.URL = "https://provider.example/second"
	second.Response.HTTP.Body.Text = `{"different":true}`
	consumer := writeCheckArchive(t, consumerCheckIdentity(), "complete", checkCapture(false, first, second))
	firstProvider := writeCheckArchive(t, buildIdentity(providerCheckIdentity(), "build-1"), "complete", checkCapture(true, cloneNormalizationFlow(t, first)))
	secondProvider := writeCheckArchive(t, buildIdentity(providerCheckIdentity(), "build-1"), "complete", checkCapture(true, cloneNormalizationFlow(t, second)))
	if mustCheck(t, Options{Mode: "consumer", Consumer: consumer, Provider: firstProvider}).Compatible {
		t.Fatal("single shard hid missing example")
	}
	consumerSet, providerSet := writeRunSet(t, consumer), writeRunSet(t, firstProvider, secondProvider)
	report, err := CheckRunSets(t.Context(), consumerSet, providerSet, "consumer", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Compatible || report.Counts.Verified != 2 || len(report.Providers) != 2 || report.Matches[0].Provider.RunID == report.Matches[1].Provider.RunID {
		t.Fatalf("lost shard evidence: %+v", report)
	}
	// Both pieces of the desired exchange exist, but in different requests.
	wrong := cloneNormalizationFlow(t, first)
	wrong.Response = second.Response
	wrong2 := cloneNormalizationFlow(t, second)
	wrong2.Response = first.Response
	p1 := writeCheckArchive(t, buildIdentity(providerCheckIdentity(), "build-1"), "complete", checkCapture(true, wrong))
	p2 := writeCheckArchive(t, buildIdentity(providerCheckIdentity(), "build-1"), "complete", checkCapture(true, wrong2))
	report, err = CheckRunSets(t.Context(), consumerSet, writeRunSet(t, p1, p2), "provider", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Compatible || report.Counts.Mismatched != 2 {
		t.Fatalf("spliced unrelated exchanges: %+v", report)
	}
}

func TestRunSetRejectsMixedBuildsDuplicateIncompleteAndChangedInputs(t *testing.T) {
	makeArchive := func(revision, state string) string {
		return writeCheckArchive(t, buildIdentity(providerCheckIdentity(), revision), state, checkCapture(true, httpNormalizationFixture()))
	}
	first := makeArchive("one", "complete")
	for name, paths := range map[string][]string{
		"different build": {first, makeArchive("two", "complete")},
		"duplicate":       {first, first},
		"incomplete":      {makeArchive("one", "open")},
		"unknown build":   {makeArchive("", "complete"), makeArchive("", "complete")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runset.Create(t.Context(), paths, filepath.Join(t.TempDir(), "set.json")); err == nil {
				t.Fatal("accepted invalid set")
			}
		})
	}
	path := writeRunSet(t, first)
	manifest := filepath.Join(first, "bundle.json")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := runset.Load(t.Context(), path); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed member accepted: %v", err)
	}
}

func TestRunSetRejectsCorruptedCollectionEvenWhenOtherShardMatches(t *testing.T) {
	consumer := writeCheckArchive(t, consumerCheckIdentity(), "complete", checkCapture(false, httpNormalizationFixture()))
	first := writeCheckArchive(t, buildIdentity(providerCheckIdentity(), "one"), "complete", checkCapture(true, httpNormalizationFixture()))
	second := writeCheckArchive(t, buildIdentity(providerCheckIdentity(), "one"), "complete", checkCapture(true, httpNormalizationFixture()))
	providerSet := writeRunSet(t, first, second)
	files, err := filepath.Glob(filepath.Join(second, "exports", "postman", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("missing fixture: %v", err)
	}
	if err := os.WriteFile(files[0], []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckRunSets(t.Context(), writeRunSet(t, consumer), providerSet, "consumer", "", nil); err == nil {
		t.Fatal("corrupt shard was hidden")
	}
}

func TestRunSetCheckRetainsHeaderPolicyAndStableWitness(t *testing.T) {
	flow := httpNormalizationFixture()
	consumer := writeCheckArchive(t, consumerCheckIdentity(), "complete", checkCapture(false, cloneNormalizationFlow(t, flow)))
	first := writeCheckArchive(t, buildIdentity(providerCheckIdentity(), "one"), "complete", checkCapture(true, cloneNormalizationFlow(t, flow)))
	second := writeCheckArchive(t, buildIdentity(providerCheckIdentity(), "one"), "complete", checkCapture(true, cloneNormalizationFlow(t, flow)))
	consumerSet := writeRunSet(t, consumer)
	providerSet := writeRunSet(t, first, second)
	forward, err := CheckRunSets(t.Context(), consumerSet, providerSet, "consumer", "", []string{"X-Build-Noise"})
	if err != nil {
		t.Fatal(err)
	}
	var manifest runset.Manifest
	data, err := os.ReadFile(providerSet)
	if err != nil || json.Unmarshal(data, &manifest) != nil {
		t.Fatalf("read fixture manifest: %v", err)
	}
	manifest.Members[0], manifest.Members[1] = manifest.Members[1], manifest.Members[0]
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(providerSet, data, 0600); err != nil {
		t.Fatal(err)
	}
	backward, err := CheckRunSets(t.Context(), consumerSet, providerSet, "consumer", "", []string{"X-Build-Noise"})
	if err != nil {
		t.Fatal(err)
	}
	if !forward.Compatible || !backward.Compatible || !reflect.DeepEqual(forward.Matches, backward.Matches) || !reflect.DeepEqual(forward.Providers, backward.Providers) {
		t.Fatalf("member order changed the chosen witness: %+v, %+v", forward, backward)
	}
	if len(forward.IgnoredHeaders) <= len(IgnoredHeaders) || !strings.Contains(strings.Join(forward.IgnoredHeaders, ","), "x-build-noise") {
		t.Fatalf("run set lost its effective comparison policy: %+v", forward.IgnoredHeaders)
	}
}

func TestRunSetRejectsChangedFrozenContractsWithinOneBuild(t *testing.T) {
	first := writeCheckArchive(t, buildIdentity(providerCheckIdentity(), "one"), "complete", httpRulesCapture(t, true,
		httpRulesDocument("response", map[string]any{"type": "string", "enum": []any{"ready", "pending"}}), `"ready"`))
	second := writeCheckArchive(t, buildIdentity(providerCheckIdentity(), "one"), "complete", httpRulesCapture(t, true,
		httpRulesDocument("response", map[string]any{"type": "string", "enum": []any{"ready", "pending", "cancelled"}}), `"ready"`))
	if _, err := runset.Create(t.Context(), []string{first, second}, filepath.Join(t.TempDir(), "set.json")); err == nil || !strings.Contains(err.Error(), "frozen contracts") {
		t.Fatalf("matching examples hid a changed frozen contract: %v", err)
	}
}

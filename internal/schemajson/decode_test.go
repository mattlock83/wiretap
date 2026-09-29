package schemajson

import (
	"encoding/json"
	"testing"
)

func TestExactNumbersAndOrdinaryScalars(t *testing.T) {
	for raw, expected := range map[string]string{
		"0.10000000000000001": "0.10000000000000001",
		"1.000e-1":            "0.1",
		".1":                  "0.1",
		"1.":                  "1",
		"-0.00":               "0",
		"0x10":                "16",
		"0o12":                "10",
		"012":                 "10",
		"0b11":                "3",
		"1_000":               "1000",
		"9007199254740993":    "9007199254740993",
		"+1.20e2":             "120",
		"true":                "true",
		"null":                "null",
		`"0.1"`:               `"0.1"`,
	} {
		t.Run(raw, func(t *testing.T) {
			value, err := Decode([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(value)
			if err != nil || string(encoded) != expected {
				t.Fatalf("got %s, %v; expected %s", encoded, err, expected)
			}
		})
	}
	for _, raw := range []string{".nan", ".inf", "-.inf"} {
		if _, err := Decode([]byte(raw)); err == nil {
			t.Errorf("accepted non-finite JSON number %s", raw)
		}
	}
}

func TestExactNumbersPreserveYAMLMergeOverrides(t *testing.T) {
	data := []byte(`base: &base
  type: string
  default: inherited
  minimum: 0.10000000000000001
  nested: {inherited: true}
fallback: &fallback
  type: boolean
  minimum: 2
schema:
  <<: [*base, *fallback]
  type: integer
  default: null
  nested: {explicit: true}
copy: *base
`)
	value, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	doc := value.(map[string]any)
	schema := doc["schema"].(map[string]any)
	if schema["type"] != "integer" || schema["default"] != nil || schema["minimum"] != json.Number("0.10000000000000001") {
		t.Fatalf("YAML merge precedence changed: %+v", schema)
	}
	nested := schema["nested"].(map[string]any)
	if len(nested) != 1 || nested["explicit"] != true {
		t.Fatalf("explicit nested object was merged instead of replaced: %+v", nested)
	}
	if doc["copy"].(map[string]any)["minimum"] != json.Number("0.10000000000000001") {
		t.Fatal("alias lost numeric precision")
	}
}

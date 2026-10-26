package strictjson

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDecodePreservesExactValues(t *testing.T) {
	value, err := Decode([]byte(` {"large":18446744073709551615,"exponent":1.2300e999999,"empty":[],"nested":[null,true,{"text":"😀"}]} `), Options{MaxDepth: 8, MaxNodes: 20, Unicode: RequireValidUnicode})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"large": json.Number("18446744073709551615"), "exponent": json.Number("1.2300e999999"),
		"empty": []any{}, "nested": []any{nil, true, map[string]any{"text": "😀"}},
	}
	if !reflect.DeepEqual(value, want) {
		t.Fatalf("JSON values changed: %#v", value)
	}
}

func TestDecodeRejectsAmbiguousOrMalformedDocuments(t *testing.T) {
	for _, raw := range []string{
		`{"x":null,"x":1}`, `{"nested":[{"a":1,"\u0061":2}]}`,
		`{"😀":1,"\ud83d\ude00":2}`, `{"open":`, `{"x":1,}`, `[1,]`,
		`[}`, `{"x":0]`, ``, `1 2`, `null {}`, `true garbage`,
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := Decode([]byte(raw), Options{MaxDepth: 8, MaxNodes: 20}); err == nil {
				t.Fatal("accepted invalid or ambiguous JSON")
			}
		})
	}
}

func TestDecodeUnicodePolicies(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string
		utf8, unicode   bool
	}{
		{"UTF-8", `"😀"`, "😀", true, true},
		{"pair", `"\ud83d\ude00"`, "😀", true, true},
		{"literal escape", `"\\ud800"`, `\ud800`, true, true},
		{"escaped quote", `"\"\ud83d\ude00"`, "\"😀", true, true},
		{"high surrogate", `"\ud800"`, "�", true, false},
		{"low surrogate", `"\udc00"`, "�", true, false},
		{"wrong pair", `"\ud800\ud800"`, "��", true, false},
		{"invalid UTF-8", string([]byte{'"', 0xff, '"'}), "�", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, policy := range []UnicodePolicy{ReplaceInvalidUnicode, RequireUTF8, RequireValidUnicode} {
				value, err := Decode([]byte(tc.raw), Options{MaxDepth: 0, MaxNodes: 1, Unicode: policy})
				allowed := policy == ReplaceInvalidUnicode || policy == RequireUTF8 && tc.utf8 || policy == RequireValidUnicode && tc.unicode
				if allowed && (err != nil || value != tc.want) {
					t.Fatalf("policy %d: got %q, %v; want %q", policy, value, err, tc.want)
				}
				if !allowed && err == nil {
					t.Fatalf("policy %d accepted malformed Unicode", policy)
				}
			}
		})
	}
}

func TestDecodeEnforcesValueAndDepthLimits(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		options   Options
		valid     bool
	}{
		{"root scalar", `0`, Options{MaxDepth: 0, MaxNodes: 1}, true},
		{"root container", `{}`, Options{MaxDepth: 0, MaxNodes: 1}, true},
		{"at depth", `[{"a":0}]`, Options{MaxDepth: 2, MaxNodes: 3}, true},
		{"over depth", `[{"a":0}]`, Options{MaxDepth: 1, MaxNodes: 3}, false},
		{"at nodes", `{"a":0,"b":1}`, Options{MaxDepth: 1, MaxNodes: 3}, true},
		{"over nodes", `{"a":0,"b":1}`, Options{MaxDepth: 1, MaxNodes: 2}, false},
		{"missing limits", `0`, Options{}, false},
		{"negative depth", `0`, Options{MaxDepth: -1, MaxNodes: 1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode([]byte(tc.raw), tc.options)
			if (err == nil) != tc.valid {
				t.Fatalf("got %v; valid %t", err, tc.valid)
			}
		})
	}
	// A small caller limit must stop before exhausting the runtime stack even
	// when the input itself is much more deeply nested.
	if _, err := Decode([]byte(strings.Repeat("[", 20000)), Options{MaxDepth: 64, MaxNodes: 100}); err == nil {
		t.Fatal("unbounded nesting accepted")
	}
}

func TestDecodeRetainsStandardJSONNestingCeiling(t *testing.T) {
	for _, tc := range []struct {
		containers int
		leaf       string
		valid      bool
	}{
		{10000, "0", true},
		{10000, "", true},
		{10001, "", false},
	} {
		raw := []byte(strings.Repeat("[", tc.containers) + tc.leaf + strings.Repeat("]", tc.containers))
		_, err := Decode(raw, Options{MaxDepth: 10000, MaxNodes: len(raw) + 1})
		if (err == nil) != tc.valid {
			t.Fatalf("%d containers with leaf %q: %v", tc.containers, tc.leaf, err)
		}
	}
}

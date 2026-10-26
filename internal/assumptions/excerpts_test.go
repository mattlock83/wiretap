package assumptions

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestDifferenceExcerptsPreserveNumbersMissingAndEscapedPaths(t *testing.T) {
	left := `{"a/b":{"~key":9007199254740993},"missing":null}`
	right := `{"a/b":{"~key":9007199254740992}}`
	paths, got := jsonDifferences(left, right, "response")
	if len(paths) != 2 || len(got) != 2 || paths[0] != got[0].Path || paths[1] != got[1].Path {
		t.Fatalf("paths and excerpts disagree: %v / %+v", paths, got)
	}
	if len(got) != 2 || got[0].Path != "response/a~1b/~0key" || got[0].Expected != "9007199254740993" || got[0].Actual != "9007199254740992" || got[1].Expected != "null" || got[1].Actual != "<missing>" {
		t.Fatalf("lost difference: %+v", got)
	}
	if value := excerpt(strings.Repeat("é", 400), true); len([]rune(value)) != 257 {
		t.Fatalf("unbounded excerpt: %d", len([]rune(value)))
	}
}

func TestJSONDifferencesKeepArrayAndScalarDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, left, right string
		want              []Difference
	}{
		{"array element", `{"items":[1,null]}`, `{"items":[1,false]}`, []Difference{{"response/items/1", "null", "false"}}},
		{"array length", `{"items":[1]}`, `{"items":[1,2]}`, []Difference{{"response/items", "[1]", "[1,2]"}}},
		{"type", `{"items":{}}`, `{"items":[]}`, []Difference{{"response/items", "{}", "[]"}}},
		{"added null", `{}`, `{"new":null}`, []Difference{{"response/new", "<missing>", "null"}}},
		{"scalar", `true`, `false`, []Difference{{"response", "true", "false"}}},
		{"unparsed", `not JSON`, `null`, []Difference{{"response", `"not JSON"`, "null"}}},
		{"equal fallback", `1`, `1`, []Difference{{"response", "1", "1"}}},
		{"control characters", `"a\nb"`, `"c"`, []Difference{{"response", `"a\nb"`, `"c"`}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths, differences := jsonDifferences(tc.left, tc.right, "response")
			if !reflect.DeepEqual(differences, tc.want) || len(paths) != len(differences) {
				t.Fatalf("got %v / %+v; want %+v", paths, differences, tc.want)
			}
			for i, difference := range differences {
				if paths[i] != difference.Path {
					t.Fatal("paths and excerpts disagree")
				}
			}
		})
	}
}

func TestJSONDifferencesRetainStableOrderAndBudget(t *testing.T) {
	left, right := map[string]any{}, map[string]any{}
	for i := 29; i >= 0; i-- {
		left[fmt.Sprintf("key-%02d", i)] = i
		right[fmt.Sprintf("key-%02d", i)] = -1
	}
	l, _ := json.Marshal(left)
	r, _ := json.Marshal(right)
	paths, differences := jsonDifferences(string(l), string(r), "contract")
	if len(paths) != 20 || len(differences) != 20 {
		t.Fatalf("diagnostic budget changed: %d paths, %d excerpts", len(paths), len(differences))
	}
	for i, difference := range differences {
		if paths[i] != fmt.Sprintf("contract/key-%02d", i) || difference.Path != paths[i] || difference.Expected != fmt.Sprint(i) || difference.Actual != "-1" {
			t.Fatalf("unstable diagnostic at %d: %s / %+v", i, paths[i], difference)
		}
	}
}

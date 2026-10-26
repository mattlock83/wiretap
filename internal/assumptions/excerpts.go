package assumptions

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// jsonDifferences derives paths and bounded excerpts from the same comparison.
// Inputs are already normalized observations or validation-rule projections.
func jsonDifferences(expected, actual, root string) (paths []string, excerpts []Difference) {
	decode := func(data string) (any, error) {
		var value any
		d := json.NewDecoder(strings.NewReader(data))
		d.UseNumber()
		if err := d.Decode(&value); err != nil {
			return data, err
		}
		return value, nil
	}
	left, leftErr := decode(expected)
	right, rightErr := decode(actual)
	add := func(path string, a, b any, aExists, bExists bool) {
		if len(paths) < 20 {
			paths = append(paths, path)
			excerpts = append(excerpts, Difference{path, excerpt(a, aExists), excerpt(b, bExists)})
		}
	}
	var walk func(any, any, string)
	walk = func(a, b any, path string) {
		if len(paths) >= 20 || reflect.DeepEqual(a, b) {
			return
		}
		am, aok := a.(map[string]any)
		bm, bok := b.(map[string]any)
		if aok && bok {
			keys := make([]string, 0, len(am)+len(bm))
			for key := range am {
				keys = append(keys, key)
			}
			for key := range bm {
				if _, exists := am[key]; !exists {
					keys = append(keys, key)
				}
			}
			sort.Strings(keys)
			for _, key := range keys {
				av, ae := am[key]
				bv, be := bm[key]
				child := path + "/" + strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
				if ae != be {
					add(child, av, bv, ae, be)
				} else {
					walk(av, bv, child)
				}
			}
			return
		}
		aa, aok := a.([]any)
		ba, bok := b.([]any)
		if aok && bok && len(aa) == len(ba) {
			for i := range aa {
				walk(aa[i], ba[i], path+"/"+strconv.Itoa(i))
			}
			return
		}
		add(path, a, b, true, true)
	}
	if leftErr == nil && rightErr == nil {
		walk(left, right, root)
	}
	if len(paths) == 0 {
		add(root, left, right, true, true)
	}
	return paths, excerpts
}

func excerpt(value any, exists bool) string {
	if !exists {
		return "<missing>"
	}
	encoded, _ := json.Marshal(value)
	// Rune truncation preserves UTF-8; JSON escaping makes control bytes visible.
	text := []rune(string(encoded))
	if len(text) > 256 {
		return string(text[:256]) + "…"
	}
	return string(text)
}

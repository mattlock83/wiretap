package assumptions

import (
	"encoding/json"
	"strconv"
	"strings"
)

func differenceExcerpts(expected, actual string, paths []string) []Difference {
	decode := func(data string) any {
		var value any
		d := json.NewDecoder(strings.NewReader(data))
		d.UseNumber()
		if d.Decode(&value) != nil {
			return data
		}
		return value
	}
	left, right := decode(expected), decode(actual)
	out := []Difference{}
	for _, path := range paths {
		if len(out) == 20 {
			break
		}
		parts := strings.Split(strings.TrimPrefix(path, "response"), "/")[1:]
		out = append(out, Difference{path, excerptAt(left, parts), excerptAt(right, parts)})
	}
	return out
}

func excerptAt(value any, parts []string) string {
	for _, part := range parts {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch container := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = container[part]
			if !ok {
				return "<missing>"
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(container) {
				return "<missing>"
			}
			value = container[index]
		default:
			return "<missing>"
		}
	}
	encoded, _ := json.Marshal(value)
	// Rune truncation preserves UTF-8; JSON escaping makes control bytes visible.
	text := []rune(string(encoded))
	if len(text) > 256 {
		return string(text[:256]) + "…"
	}
	return string(text)
}

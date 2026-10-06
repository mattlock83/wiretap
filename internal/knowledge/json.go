// Package knowledge publishes and reads repository application-knowledge bundles.
// Immutable capture archives remain the responsibility of package bundle.
package knowledge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const Version = "1.0.0-draft.1"
const Owner = "dev.phrond.wiretap"
const RecordingID = "urn:phrond:wiretap:recording"
const RecordingVersion = "1.0.0-draft.1"

type Object = map[string]any

func object(v any) Object  { m, _ := v.(map[string]any); return m }
func str(v any) string     { s, _ := v.(string); return s }
func list(v any) []any     { a, _ := v.([]any); return a }
func hash(b []byte) string { d := sha256.Sum256(b); return hex.EncodeToString(d[:]) }

// Decode rejects duplicate keys, invalid Unicode, trailing documents and lossy
// number conversion. Captured numeric values retain their original precision.
func Decode(b []byte) (any, error) {
	if !utf8.Valid(b) || bytes.HasPrefix(b, []byte{0xef, 0xbb, 0xbf}) {
		return nil, fmt.Errorf("invalid UTF-8 or BOM")
	}
	// encoding/json repairs lone UTF-16 surrogates. The interchange format
	// rejects them instead of silently accepting changed string data.
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' {
			continue
		}
		if i+1 >= len(b) {
			break
		}
		if b[i+1] != 'u' {
			i++
			continue
		}
		if i+5 >= len(b) {
			break
		}
		n, err := strconv.ParseUint(string(b[i+2:i+6]), 16, 16)
		if err != nil {
			continue
		}
		if n >= 0xdc00 && n <= 0xdfff {
			return nil, fmt.Errorf("unpaired Unicode surrogate")
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+11 >= len(b) || string(b[i+6:i+8]) != `\u` {
				return nil, fmt.Errorf("unpaired Unicode surrogate")
			}
			low, err := strconv.ParseUint(string(b[i+8:i+12]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return nil, fmt.Errorf("unpaired Unicode surrogate")
			}
			i += 11
		} else {
			i += 5
		}
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	depth := 0
	var read func() (any, error)
	read = func() (any, error) {
		depth++
		defer func() { depth-- }()
		if depth > 256 {
			return nil, fmt.Errorf("READ-02 JSON nesting limit exceeded")
		}
		t, err := d.Token()
		if err != nil {
			return nil, err
		}
		switch t {
		case json.Delim('{'):
			m := Object{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return nil, err
				}
				key := k.(string)
				if _, ok := m[key]; ok {
					return nil, fmt.Errorf("duplicate JSON key %q", key)
				}
				v, err := read()
				if err != nil {
					return nil, err
				}
				m[key] = v
			}
			_, err = d.Token()
			return m, err
		case json.Delim('['):
			a := []any{}
			for d.More() {
				v, err := read()
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			_, err = d.Token()
			return a, err
		default:
			return t, nil
		}
	}
	v, err := read()
	if err != nil {
		return nil, err
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON data")
	}
	return v, nil
}

// Canonical implements the profile JSON rules, including literal non-ASCII and
// HTML characters. Encoding from maps ensures sorted object keys even for structs.
func Canonical(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	v, err = Decode(b)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	e := json.NewEncoder(&out)
	e.SetEscapeHTML(false)
	e.SetIndent("", "  ")
	if err = e.Encode(v); err != nil {
		return nil, err
	}
	// Only unescaped JSON escapes are replaced; a literal backslash-u string must
	// remain distinct from the Unicode character it describes.
	b = out.Bytes()
	var clean bytes.Buffer
	for i := 0; i < len(b); i++ {
		if b[i] == '\\' && i+1 < len(b) {
			if i+5 < len(b) && (string(b[i:i+6]) == `\u2028` || string(b[i:i+6]) == `\u2029`) {
				if b[i+5] == '8' {
					clean.WriteString("\u2028")
				} else {
					clean.WriteString("\u2029")
				}
				i += 5
				continue
			}
			clean.WriteByte(b[i])
			i++
			clean.WriteByte(b[i])
			continue
		}
		clean.WriteByte(b[i])
	}
	return clean.Bytes(), nil
}

var pathPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)*$`)
var keyPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func reserved(s string) bool {
	s = strings.ToLower(strings.Split(s, ".")[0])
	return s == "con" || s == "prn" || s == "aux" || s == "nul" || regexp.MustCompile(`^(com|lpt)[1-9]$`).MatchString(s)
}
func safePath(p string) bool {
	if len(p) > 240 || !pathPattern.MatchString(p) {
		return false
	}
	for _, s := range strings.Split(p, "/") {
		if len(s) > 128 || s == "." || s == ".." || strings.HasSuffix(s, ".") || reserved(s) {
			return false
		}
	}
	return true
}
func safeKey(s string) bool { return len(s) <= 64 && keyPattern.MatchString(s) && !reserved(s) }
func number(v any) int {
	n, _ := v.(json.Number)
	i, err := n.Int64()
	if err != nil || i < 0 || i > 9007199254740991 {
		return -1
	}
	return int(i)
}

// Only normalize fields owned by this version of the profile. Unknown optional
// objects can contain numbers and ordered arrays with their own semantics.
func coreNumbers(v any) error {
	_, err := coreCanonical(v)
	return err
}

func coreCanonical(v any) ([]byte, error) {
	b, err := Canonical(v)
	if err != nil {
		return nil, err
	}
	value, err := Decode(b)
	if err != nil {
		return nil, err
	}
	root := object(value)
	sets := []string{}
	counts := []string{}
	switch str(object(root["profile"])["id"]) {
	case "application-knowledge":
		sets = []string{"required_capabilities", "subjects/*/contract_ids", "contracts/*/subject_ids", "measurements/*/subject_ids"}
	case "test-result":
		sets = []string{"source_ids", "capture_scope/subject_ids"}
		counts = []string{"attempt_ordinal", "artifacts/*/retention/retained_bytes", "artifacts/*/retention/original_bytes"}
	default:
		if root["producer"] != nil && root["sources"] != nil {
			sets = []string{"target_source_ids", "selection/test_ids"}
		}
	}
	var at func(any, []string, func(any) (any, error)) error
	at = func(v any, parts []string, transform func(any) (any, error)) error {
		if parts[0] == "*" {
			for _, item := range list(v) {
				if err := at(item, parts[1:], transform); err != nil {
					return err
				}
			}
			return nil
		}
		m := object(v)
		x, exists := m[parts[0]]
		if !exists {
			return nil
		}
		if len(parts) > 1 {
			return at(x, parts[1:], transform)
		}
		y, err := transform(x)
		if err == nil {
			m[parts[0]] = y
		}
		return err
	}
	for _, p := range sets {
		if err := at(value, strings.Split(p, "/"), func(v any) (any, error) {
			a := list(v)
			sort.Slice(a, func(i, j int) bool { return str(a[i]) < str(a[j]) })
			return v, nil
		}); err != nil {
			return nil, err
		}
	}
	for _, p := range counts {
		if err := at(value, strings.Split(p, "/"), func(v any) (any, error) {
			n, ok := v.(json.Number)
			if !ok {
				return nil, fmt.Errorf("core count must be a number")
			}
			r, ok := new(big.Rat).SetString(string(n))
			if !ok || !r.IsInt() || r.Num().Sign() < 0 || r.Num().BitLen() > 53 {
				return nil, fmt.Errorf("core counts must be nonnegative exact safe integers")
			}
			return json.Number(r.Num().String()), nil
		}); err != nil {
			return nil, err
		}
	}
	return Canonical(value)
}

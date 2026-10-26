// Package strictjson reads one bounded JSON value without losing numbers or
// silently replacing duplicate object members.
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

// These errors distinguish uncertain input from ordinary JSON syntax errors.
var (
	ErrLimit        = errors.New("JSON exceeds its nesting or value limit")
	ErrDuplicateKey = errors.New("duplicate JSON key")
	ErrUnicode      = errors.New("invalid JSON Unicode")
)

// UnicodePolicy controls the encoding/json replacement behavior for malformed
// strings. Callers comparing retained evidence must use RequireValidUnicode.
type UnicodePolicy uint8

const (
	ReplaceInvalidUnicode UnicodePolicy = iota
	RequireUTF8
	RequireValidUnicode
)

type Options struct {
	// MaxDepth counts nested values, with the document's root at depth zero.
	MaxDepth int
	// MaxNodes counts values, including containers but excluding object keys.
	MaxNodes int
	Unicode  UnicodePolicy
}

// Decode returns maps, slices, json.Number values and ordinary JSON scalars.
// Limits are required, including for callers whose input size already bounds
// the possible number of values.
func Decode(data []byte, options Options) (any, error) {
	if options.MaxDepth < 0 || options.MaxNodes < 1 || options.Unicode > RequireValidUnicode {
		return nil, errors.New("invalid JSON decoding limits or Unicode policy")
	}
	if options.Unicode == RequireUTF8 && !utf8.Valid(data) {
		return nil, fmt.Errorf("%w: JSON must contain valid UTF-8", ErrUnicode)
	}
	if options.Unicode == RequireValidUnicode && !validUnicode(data) {
		return nil, fmt.Errorf("%w: JSON must contain valid UTF-8 and paired Unicode surrogate escapes", ErrUnicode)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	nodes := 0
	var read func(int) (any, error)
	read = func(depth int) (any, error) {
		nodes++
		if depth > options.MaxDepth || nodes > options.MaxNodes {
			return nil, ErrLimit
		}
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		delimiter, compound := token.(json.Delim)
		if !compound {
			return token, nil
		}
		// Match encoding/json's syntax nesting ceiling, including empty
		// containers. Primitive values may sit one level below a container.
		if depth >= 10000 {
			return nil, ErrLimit
		}
		switch delimiter {
		case '{':
			object := map[string]any{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("JSON object key must be a string")
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("%w %q", ErrDuplicateKey, key)
				}
				object[key], err = read(depth + 1)
				if err != nil {
					return nil, err
				}
			}
			if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
				return nil, errors.New("invalid JSON object")
			}
			return object, nil
		case '[':
			array := []any{}
			for decoder.More() {
				value, err := read(depth + 1)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
				return nil, errors.New("invalid JSON array")
			}
			return array, nil
		default:
			return nil, errors.New("unexpected JSON delimiter")
		}
	}
	value, err := read(0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, errors.New("input must contain exactly one JSON value")
	}
	return value, nil
}

// encoding/json replaces invalid UTF-8 and unpaired surrogate escapes with the
// replacement character. Validate them before decoding when exact retained
// strings matter. The decoder checks the remaining JSON grammar.
func validUnicode(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	codeUnit := func(raw []byte) (uint16, bool) {
		var result uint16
		for _, c := range raw {
			result <<= 4
			switch {
			case c >= '0' && c <= '9':
				result += uint16(c - '0')
			case c >= 'a' && c <= 'f':
				result += uint16(c-'a') + 10
			case c >= 'A' && c <= 'F':
				result += uint16(c-'A') + 10
			default:
				return 0, false
			}
		}
		return result, true
	}
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		value, valid := codeUnit(raw[i+1 : i+5])
		if !valid {
			return false
		}
		i += 4
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, valid := codeUnit(raw[i+3 : i+7])
			if !valid || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		} else if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
	}
	return true
}

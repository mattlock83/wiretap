package assumptions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"sort"
	"strings"

	"github.com/mattlock83/wiretap/internal/strictjson"
)

const (
	maxProjectionBytes  = 64 << 20
	maxProjectionsBytes = 256 << 20
	maxProjectionFiles  = 1024
	maxProjectionDepth  = 64
	maxProjectionNodes  = 100000
)

func readProjection(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("must be a regular file")
	}
	if info.Size() > maxProjectionBytes {
		return nil, fmt.Errorf("exceeds %d MiB", maxProjectionBytes>>20)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxProjectionBytes+1))
	if err == nil && len(data) > maxProjectionBytes {
		err = fmt.Errorf("exceeds %d MiB", maxProjectionBytes>>20)
	}
	return data, err
}

func decodeProjectionJSON(data []byte) (any, error) {
	return strictjson.Decode(data, strictjson.Options{
		MaxDepth: maxProjectionDepth, MaxNodes: maxProjectionNodes,
		Unicode: strictjson.RequireValidUnicode,
	})
}

func writeCanonicalJSON(out *bytes.Buffer, value any) error {
	switch value := value.(type) {
	case json.Number:
		number, err := canonicalNumber(string(value))
		if err != nil {
			return err
		}
		out.WriteString(number)
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for i, key := range keys {
			if i != 0 {
				out.WriteByte(',')
			}
			encoded, _ := json.Marshal(key)
			out.Write(encoded)
			out.WriteByte(':')
			if err := writeCanonicalJSON(out, value[key]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	case []any:
		out.WriteByte('[')
		for i, item := range value {
			if i != 0 {
				out.WriteByte(',')
			}
			if err := writeCanonicalJSON(out, item); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		out.Write(encoded)
	}
	return nil
}

// Keep numeric precision and normalize equivalent decimal spellings without
// expanding exponents (including deliberately huge exponents in test data).
func canonicalNumber(raw string) (string, error) {
	if len(raw) > 1024 {
		return "", errors.New("JSON number exceeds normalization limit")
	}
	sign := ""
	if strings.HasPrefix(raw, "-") {
		sign, raw = "-", raw[1:]
	}
	exponent := new(big.Int)
	if index := strings.IndexAny(raw, "eE"); index >= 0 {
		if _, ok := exponent.SetString(raw[index+1:], 10); !ok {
			return "", errors.New("invalid JSON exponent")
		}
		raw = raw[:index]
	}
	if index := strings.IndexByte(raw, '.'); index >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(raw)-index-1)))
		raw = raw[:index] + raw[index+1:]
	}
	raw = strings.TrimLeft(raw, "0")
	if raw == "" {
		return "0", nil
	}
	trimmed := strings.TrimRight(raw, "0")
	exponent.Add(exponent, big.NewInt(int64(len(raw)-len(trimmed))))
	return sign + trimmed + "e" + exponent.String(), nil
}

package knowledge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// Config is an explicit identity and selection map, kept in the application
// repository. Subjects use the public manifest shape; the publisher supplies
// conventional page paths. No identity is derived from an execution or digest.
type Config struct {
	Version         int               `json:"version"`
	BundleID        string            `json:"bundle_id"`
	Application     Object            `json:"application"`
	InventoryScope  string            `json:"inventory_scope"`
	Subjects        []Object          `json:"subjects"`
	Contracts       []ContractMapping `json:"contracts"`
	Tests           []TestMapping     `json:"tests"`
	Selection       Object            `json:"selection"`
	Sources         []Object          `json:"sources,omitempty"`
	TargetSourceIDs []string          `json:"target_source_ids,omitempty"`
	RemovedSubjects []Object          `json:"removed_subjects,omitempty"`
	Migrations      []Object          `json:"migrations,omitempty"`
}
type ContractMapping struct {
	SchemaID     string            `json:"schema_id"`
	SourceSHA256 string            `json:"source_sha256,omitempty"`
	ID           string            `json:"id"`
	Key          string            `json:"key"`
	SubjectIDs   []string          `json:"subject_ids"`
	Origin       string            `json:"origin"`
	Files        map[string]string `json:"files,omitempty"`
}
type TestMapping struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Group           string         `json:"group,omitempty"`
	AttemptOrdinals map[string]int `json:"attempt_ordinals,omitempty"`
}

func ReadConfig(p string) (Config, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return Config{}, err
	}
	if _, err := Decode(b); err != nil {
		return Config{}, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	d.UseNumber()
	var c Config
	if err := d.Decode(&c); err != nil {
		return c, err
	}
	if c.Version != 1 {
		return c, fmt.Errorf("unsupported publication configuration version %d", c.Version)
	}
	return c, nil
}

// merge keeps unknown producer-neutral fields while replacing declared fields.
// Captured arrays are position-preserving; they are never sorted as sets here.
func merge(old, next any) any {
	if n := object(next); n != nil {
		o := object(old)
		result := Object{}
		for k, v := range o {
			result[k] = v
		}
		for k, v := range n {
			result[k] = merge(o[k], v)
		}
		return result
	}
	return next
}

func mergeCapture(old, next any) any {
	if n := object(next); n != nil {
		o := object(old)
		result := Object{}
		for k, v := range o {
			result[k] = v
		}
		for k, v := range n {
			result[k] = mergeCapture(o[k], v)
		}
		return result
	}
	if n, ok := next.([]any); ok {
		o := list(old)
		result := make([]any, len(n))
		for i, v := range n {
			if i < len(o) {
				result[i] = mergeCapture(o[i], v)
			} else {
				result[i] = v
			}
		}
		return result
	}
	return next
}

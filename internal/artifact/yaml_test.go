package artifact

import (
	"testing"
	"time"
)

func TestEncodeDeterministic(t *testing.T) {
	baseTime := time.Date(2026, 2, 4, 19, 0, 0, 0, time.UTC)

	artifactA := Artifact{
		Version: 1,
		TestCase: TestCase{
			ID:        "tc-1",
			Name:      "deterministic",
			StartedAt: baseTime,
		},
		Traffic: []Flow{
			{
				ID:       "b",
				Protocol: "http",
				Channel:  "test_to_sut",
				Request: Message{HTTP: &HttpMessage{
					Method: "GET",
					URL:    "http://example.com",
					Headers: Headers{
						"b": {"2"},
						"a": {"1"},
					},
				}},
				Response: Message{HTTP: &HttpMessage{Status: 200}},
			},
			{
				ID:       "a",
				Protocol: "http",
				Channel:  "test_to_sut",
				Request:  Message{HTTP: &HttpMessage{Method: "GET", URL: "http://example.com/second"}},
				Response: Message{HTTP: &HttpMessage{Status: 200}},
			},
		},
	}

	artifactB := Artifact{
		Version: 1,
		TestCase: TestCase{
			ID:        "tc-1",
			Name:      "deterministic",
			StartedAt: baseTime,
		},
		Traffic: []Flow{
			{
				ID:       "a",
				Protocol: "http",
				Channel:  "test_to_sut",
				Request: Message{HTTP: &HttpMessage{
					Method: "GET",
					URL:    "http://example.com/second",
				}},
				Response: Message{HTTP: &HttpMessage{Status: 200}},
			},
			{
				ID:       "b",
				Protocol: "http",
				Channel:  "test_to_sut",
				Request: Message{HTTP: &HttpMessage{
					Method: "GET",
					URL:    "http://example.com",
					Headers: Headers{
						"a": {"1"},
						"b": {"2"},
					},
				}},
				Response: Message{HTTP: &HttpMessage{Status: 200}},
			},
		},
	}

	outA, err := EncodeBytes(&artifactA)
	if err != nil {
		t.Fatalf("encode artifact A: %v", err)
	}
	outB, err := EncodeBytes(&artifactB)
	if err != nil {
		t.Fatalf("encode artifact B: %v", err)
	}
	if string(outA) != string(outB) {
		t.Fatalf("expected deterministic output")
	}
}

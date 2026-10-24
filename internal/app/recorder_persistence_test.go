package app

import (
	"errors"
	"testing"

	"github.com/mattlock83/wiretap/internal/artifact"
)

type failOnceCaptureSink struct {
	commits int
	id      string
}

func (s *failOnceCaptureSink) Register(_ artifact.TestCase, refs []artifact.SchemaRef) ([]artifact.SchemaRef, error) {
	return refs, nil
}

func (s *failOnceCaptureSink) Commit(art *artifact.Artifact) (string, error) {
	s.commits++
	if s.id != "" && s.id != art.TestCase.ID {
		return "", errors.New("retry changed attempt identity")
	}
	s.id = art.TestCase.ID
	if s.commits == 1 {
		return "", errors.New("temporary write failure")
	}
	return "tests/" + s.id + ".json", nil
}

func TestPersistenceRetryRetainsFinalizedAttempt(t *testing.T) {
	sink := &failOnceCaptureSink{}
	store := NewTestCaseStore(sink, 1024)
	if _, err := store.StartCase("retry", nil, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterActive(); err != nil {
		t.Fatal(err)
	}
	first, _, err := store.FinishCase("pass", nil)
	if err == nil || first == nil || !store.HasPendingCommit() {
		t.Fatalf("wanted retained failed commit, got %v", err)
	}
	if _, err := store.StartCase("next", nil, nil, ""); err == nil {
		t.Fatal("new test discarded pending capture")
	}
	second, path, err := store.FinishCase("ignored-on-retry", nil)
	if err != nil {
		t.Fatal(err)
	}
	if path == "" || first.TestCase.ID != second.TestCase.ID || second.TestCase.Status != "pass" || store.HasPendingCommit() {
		t.Fatal("retry changed finalized attempt")
	}
	if sink.commits != 2 {
		t.Fatalf("wanted exactly two commit attempts, got %d", sink.commits)
	}
}

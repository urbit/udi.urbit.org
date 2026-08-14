package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDraftSnapshotValid(t *testing.T) {
	if err := DraftSnapshot().Validate(); err != nil {
		t.Fatalf("DraftSnapshot.Validate() error = %v", err)
	}
}

func TestCompleteSnapshotValidation(t *testing.T) {
	snapshot := completeSnapshot()
	if err := snapshot.Validate(); err != nil {
		t.Fatalf("complete snapshot error = %v", err)
	}

	tests := []struct {
		name string
		edit func(*Snapshot)
		want string
	}{
		{"missing config digest", func(s *Snapshot) { s.ConfigDigest = "" }, "configDigest"},
		{"partial coverage", func(s *Snapshot) { s.Coverage.Complete = false }, "complete collection coverage"},
		{"active exceeds all time", func(s *Snapshot) { s.Metrics.ActiveContributors.Count = Int(31) }, "active contributors cannot exceed"},
		{"core exceeds ecosystem", func(s *Snapshot) {
			m := s.CoreRepositories["urbit/vere"]
			m.AllTimeContributors.Count = Int(31)
			s.CoreRepositories["urbit/vere"] = m
		}, "cannot exceed ecosystem totals"},
		{"bad unit", func(s *Snapshot) { s.Metrics.ActiveRepositories.Unit = "repos" }, "unit must be count"},
		{"extra core repo", func(s *Snapshot) { s.CoreRepositories["example/repo"] = RepositoryMetrics{} }, "exactly two"},
		{"changed definition", func(s *Snapshot) { s.Definitions["activeContributor"] = "different" }, "approved methodology"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := completeSnapshot()
			test.edit(&candidate)
			err := candidate.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestReadRejectsTrailingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, []byte(`{} {}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("Read() error = %v, want trailing JSON error", err)
	}
}

func TestWriteAtomicRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data", "latest.json")
	want := completeSnapshot()
	if err := WriteAtomic(path, want); err != nil {
		t.Fatalf("WriteAtomic() error = %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got.ConfigDigest != want.ConfigDigest || *got.Metrics.ActiveContributors.Count != 12 {
		t.Fatalf("round-trip snapshot = %#v", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("snapshot permissions = %o, want 644", info.Mode().Perm())
	}
}

func completeSnapshot() Snapshot {
	snapshot := DraftSnapshot()
	snapshot.Status = StatusComplete
	snapshot.ConfigDigest = "sha256:test"
	snapshot.GeneratedAt = "2026-08-12T12:00:00Z"
	snapshot.ActiveWindow = Window{Start: "2026-02-12T12:00:00Z", End: "2026-08-12T12:00:00Z", Label: "trailing six months"}
	snapshot.Coverage = Coverage{Complete: true, CandidateRepositories: 12, IncludedRepositories: 7, Organizations: []string{"urbit"}}
	snapshot.Metrics = AggregateMetrics{
		ActiveRepositories:  Value{Count: Int(5), Unit: "count"},
		ActiveContributors:  Value{Count: Int(12), Unit: "count"},
		AllTimeContributors: Value{Count: Int(30), Unit: "count"},
	}
	snapshot.CoreRepositories = map[string]RepositoryMetrics{
		"urbit/urbit": {ActiveContributors: Value{Count: Int(7), Unit: "count"}, AllTimeContributors: Value{Count: Int(20), Unit: "count"}},
		"urbit/vere":  {ActiveContributors: Value{Count: Int(4), Unit: "count"}, AllTimeContributors: Value{Count: Int(15), Unit: "count"}},
	}
	return snapshot
}

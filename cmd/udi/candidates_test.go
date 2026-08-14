package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	githubcollector "github.com/urbit/udi.urbit.org/internal/github"
)

func TestCandidateReportRoundTripValidation(t *testing.T) {
	candidates := []githubcollector.CandidateRepository{
		{ID: 2, FullName: "zeta/two", HTMLURL: "https://github.com/zeta/two", Included: false, Decision: "excluded-no-hoon", Sources: []string{"organization:zeta"}},
		{ID: 1, FullName: "Alpha/one", HTMLURL: "https://github.com/Alpha/one", Included: true, Decision: "included-hoon", HasHoon: true, Sources: []string{"search:language:Hoon"}},
	}
	report := newCandidateReport(candidates, githubcollector.Coverage{CandidateRepositories: 2, IncludedRepositories: 1}, time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC), map[string]bool{"alpha/one": true})
	if err := report.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if report.Repositories[0].FullName != "Alpha/one" || report.Summary.Decisions["included-hoon"] != 1 {
		t.Fatalf("report = %#v", report)
	}
	if err := writeCandidateReport(filepath.Join(t.TempDir(), "data", "candidates.json"), report); err != nil {
		t.Fatalf("writeCandidateReport() error = %v", err)
	}
	if err := writeCandidateMarkdown(filepath.Join(t.TempDir(), "data", "candidates.md"), report); err != nil {
		t.Fatalf("writeCandidateMarkdown() error = %v", err)
	}
}

func TestCandidateReportRejectsSummaryMismatch(t *testing.T) {
	report := candidateReport{SchemaVersion: 1, GeneratedAt: "2026-08-13T12:00:00Z", Summary: candidateReportSummary{Candidates: 1}}
	if err := report.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want mismatch")
	}
}

func TestReadCandidateActivity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "candidates.json")
	if err := os.WriteFile(path, []byte(`{"repositories":[{"fullName":"Urbit/Urbit","active":true},{"fullName":"old/repo"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	activity, err := readCandidateActivity(path)
	if err != nil {
		t.Fatalf("readCandidateActivity() error = %v", err)
	}
	if !activity["urbit/urbit"] || len(activity) != 1 {
		t.Fatalf("activity = %#v", activity)
	}
}

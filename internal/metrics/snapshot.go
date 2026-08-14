// Package metrics defines the privacy-safe public data contract for the UDI site.
package metrics

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	SchemaVersion      = 1
	MethodologyVersion = "2026-08-12.1"
	StatusDraft        = "draft"
	StatusComplete     = "complete"
)

// Value is a public aggregate. A nil Count represents an intentionally
// unpublished draft value, never a measured zero.
type Value struct {
	Count *int   `json:"count"`
	Unit  string `json:"unit"`
}

// Window describes the inclusive collection period used for active metrics.
type Window struct {
	Start string `json:"start"`
	End   string `json:"end"`
	Label string `json:"label"`
}

// Coverage makes best-effort GitHub discovery limits visible to readers.
type Coverage struct {
	Complete              bool     `json:"complete"`
	CandidateRepositories int      `json:"candidateRepositories"`
	IncludedRepositories  int      `json:"includedRepositories"`
	Organizations         []string `json:"organizations"`
	Warnings              []string `json:"warnings"`
}

// AggregateMetrics contains ecosystem-wide values across every included repo.
type AggregateMetrics struct {
	ActiveRepositories  Value `json:"activeRepositories"`
	ActiveContributors  Value `json:"activeContributors"`
	AllTimeContributors Value `json:"allTimeContributors"`
}

// RepositoryMetrics provides the approved core-repository breakdown.
type RepositoryMetrics struct {
	ActiveContributors  Value `json:"activeContributors"`
	AllTimeContributors Value `json:"allTimeContributors"`
}

// Snapshot is the only data shape published by the static site. It deliberately
// excludes contributor identities, email hashes, and repository-level raw data.
type Snapshot struct {
	SchemaVersion      int                          `json:"schemaVersion"`
	MethodologyVersion string                       `json:"methodologyVersion"`
	ConfigDigest       string                       `json:"configDigest"`
	Status             string                       `json:"status"`
	GeneratedAt        string                       `json:"generatedAt"`
	ActiveWindow       Window                       `json:"activeWindow"`
	Coverage           Coverage                     `json:"coverage"`
	Metrics            AggregateMetrics             `json:"metrics"`
	CoreRepositories   map[string]RepositoryMetrics `json:"coreRepositories"`
	Definitions        map[string]string            `json:"definitions"`
}

// Definitions returns the approved public descriptions used by both the
// snapshot and rendered website.
func Definitions() map[string]string {
	return map[string]string{
		"includedRepository": "A public, non-fork, non-archived repository with verified Hoon language evidence, plus approved core exceptions.",
		"activeRepository":   "An included repository with a default-branch commit or merged pull request during the trailing six months.",
		"activeContributor":  "A unique, identifiable, non-bot GitHub commit author or merged-pull-request author active during the trailing six months across any included ecosystem repository.",
		"allTimeContributor": "A unique, identifiable, non-bot GitHub commit author or merged-pull-request author across the current included repository set.",
	}
}

// DraftSnapshot returns a valid placeholder without inventing public numbers.
func DraftSnapshot() Snapshot {
	unit := "count"
	return Snapshot{
		SchemaVersion:      SchemaVersion,
		MethodologyVersion: MethodologyVersion,
		ConfigDigest:       "",
		Status:             StatusDraft,
		GeneratedAt:        "",
		ActiveWindow:       Window{Label: "trailing six months"},
		Coverage: Coverage{
			Complete: false,
			Warnings: []string{"Draft site: run the refresh command to publish measured GitHub aggregates."},
		},
		Metrics: AggregateMetrics{
			ActiveRepositories:  Value{Unit: unit},
			ActiveContributors:  Value{Unit: unit},
			AllTimeContributors: Value{Unit: unit},
		},
		CoreRepositories: map[string]RepositoryMetrics{
			"urbit/urbit": {ActiveContributors: Value{Unit: unit}, AllTimeContributors: Value{Unit: unit}},
			"urbit/vere":  {ActiveContributors: Value{Unit: unit}, AllTimeContributors: Value{Unit: unit}},
		},
		Definitions: Definitions(),
	}
}

// Validate rejects malformed, identity-bearing, or incomplete publishable data.
func (s Snapshot) Validate() error {
	var problems []string
	if s.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Sprintf("schemaVersion is %d, want %d", s.SchemaVersion, SchemaVersion))
	}
	if s.MethodologyVersion != MethodologyVersion {
		problems = append(problems, fmt.Sprintf("methodologyVersion is %q, want %q", s.MethodologyVersion, MethodologyVersion))
	}
	if s.Status != StatusDraft && s.Status != StatusComplete {
		problems = append(problems, "status must be draft or complete")
	}
	for key, definition := range Definitions() {
		if s.Definitions[key] != definition {
			problems = append(problems, "definition does not match approved methodology: "+key)
		}
	}
	if len(s.CoreRepositories) != 2 {
		problems = append(problems, "coreRepositories must contain exactly two approved repositories")
	}
	for _, repo := range []string{"urbit/urbit", "urbit/vere"} {
		if _, ok := s.CoreRepositories[repo]; !ok {
			problems = append(problems, "coreRepositories must contain "+repo)
		}
	}

	if s.Status == StatusComplete {
		if strings.TrimSpace(s.ConfigDigest) == "" {
			problems = append(problems, "configDigest is required for complete snapshots")
		}
		if _, err := time.Parse(time.RFC3339, s.GeneratedAt); err != nil {
			problems = append(problems, "generatedAt must be an RFC3339 timestamp for complete snapshots")
		}
		if !s.Coverage.Complete {
			problems = append(problems, "complete snapshots require complete collection coverage")
		}
		windowStart, startErr := time.Parse(time.RFC3339, s.ActiveWindow.Start)
		windowEnd, endErr := time.Parse(time.RFC3339, s.ActiveWindow.End)
		if startErr != nil || endErr != nil || !windowStart.Before(windowEnd) {
			problems = append(problems, "activeWindow must contain ordered RFC3339 start and end timestamps")
		}
		if s.ActiveWindow.Label != "trailing six months" {
			problems = append(problems, "activeWindow label must match the approved trailing-six-month definition")
		}
		if s.Coverage.CandidateRepositories < 0 || s.Coverage.IncludedRepositories < 0 {
			problems = append(problems, "coverage counts cannot be negative")
		}
		if s.Coverage.IncludedRepositories > s.Coverage.CandidateRepositories {
			problems = append(problems, "included repository count cannot exceed candidate repository count")
		}
		if s.Coverage.IncludedRepositories < 2 {
			problems = append(problems, "complete snapshots must include at least the two core repositories")
		}
		values := map[string]Value{
			"metrics.activeRepositories":  s.Metrics.ActiveRepositories,
			"metrics.activeContributors":  s.Metrics.ActiveContributors,
			"metrics.allTimeContributors": s.Metrics.AllTimeContributors,
		}
		for repo, repositoryMetrics := range s.CoreRepositories {
			values[repo+".activeContributors"] = repositoryMetrics.ActiveContributors
			values[repo+".allTimeContributors"] = repositoryMetrics.AllTimeContributors
		}
		for name, value := range values {
			if value.Count == nil {
				problems = append(problems, name+" count is required")
			} else if *value.Count < 0 {
				problems = append(problems, name+" count cannot be negative")
			}
			if value.Unit != "count" {
				problems = append(problems, name+" unit must be count")
			}
		}
		if count(s.Metrics.ActiveRepositories) > s.Coverage.IncludedRepositories {
			problems = append(problems, "active repositories cannot exceed included repositories")
		}
		if count(s.Metrics.ActiveContributors) > count(s.Metrics.AllTimeContributors) {
			problems = append(problems, "active contributors cannot exceed all-time contributors")
		}
		for repo, repositoryMetrics := range s.CoreRepositories {
			if count(repositoryMetrics.ActiveContributors) > count(repositoryMetrics.AllTimeContributors) {
				problems = append(problems, repo+" active contributors cannot exceed all-time contributors")
			}
			if count(repositoryMetrics.ActiveContributors) > count(s.Metrics.ActiveContributors) || count(repositoryMetrics.AllTimeContributors) > count(s.Metrics.AllTimeContributors) {
				problems = append(problems, repo+" contributor counts cannot exceed ecosystem totals")
			}
		}
	}

	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// Read loads and validates a snapshot from disk.
func Read(path string) (Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read metric snapshot %s: %w", path, err)
	}
	var snapshot Snapshot
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return Snapshot{}, fmt.Errorf("decode metric snapshot %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Snapshot{}, fmt.Errorf("decode metric snapshot %s: unexpected trailing JSON", path)
	}
	if err := snapshot.Validate(); err != nil {
		return Snapshot{}, fmt.Errorf("validate metric snapshot %s: %w", path, err)
	}
	return snapshot, nil
}

// WriteAtomic validates and replaces a snapshot without leaving partial JSON.
func WriteAtomic(path string, snapshot Snapshot) error {
	if err := snapshot.Validate(); err != nil {
		return fmt.Errorf("refuse to write invalid metric snapshot: %w", err)
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return fmt.Errorf("encode metric snapshot: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create metric snapshot directory %s: %w", filepath.Dir(path), err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".snapshot-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary metric snapshot: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary metric snapshot: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary metric snapshot: %w", err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return fmt.Errorf("set metric snapshot permissions: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary metric snapshot: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace metric snapshot %s: %w", path, err)
	}
	return nil
}

// Int returns a stable pointer for aggregate values.
func Int(value int) *int { return &value }

func count(value Value) int {
	if value.Count == nil {
		return 0
	}
	return *value.Count
}

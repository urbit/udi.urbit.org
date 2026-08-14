package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	githubcollector "github.com/urbit/udi.urbit.org/internal/github"
)

func TestReadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repositories.json")
	data := `{
  "organizations":["urbit"],
  "explicitInclude":["urbit/urbit","urbit/vere"],
  "explicitExclude":[],
  "coreRepositories":["urbit/urbit","urbit/vere"],
  "activityWindowMonths":6,
  "maxPagesPerEndpoint":100
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, config, err := readConfig(path)
	if err != nil {
		t.Fatalf("readConfig() error = %v", err)
	}
	if string(raw) != data || len(config.CoreRepositories) != 2 || config.ActivityWindowMonths != 6 {
		t.Fatalf("readConfig() = raw %q config %#v", raw, config)
	}
}

func TestReadConfigRequiresCoreRepositoriesInExplicitIncludes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repositories.json")
	data := `{
  "organizations":["urbit"],
  "explicitInclude":["urbit/urbit","example/other"],
  "coreRepositories":["urbit/urbit","urbit/vere"],
  "activityWindowMonths":6,
  "maxPagesPerEndpoint":100
}`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readConfig(path); err == nil || !strings.Contains(err.Error(), "must also be explicitly included") {
		t.Fatalf("readConfig() error = %v", err)
	}
}

func TestToSnapshotAggregatesAllIncludedRepositories(t *testing.T) {
	config := repositoryConfig{
		Config: githubcollector.Config{
			Organizations:        []string{"urbit", "tocwex"},
			ExplicitInclude:      []string{"urbit/urbit", "urbit/vere"},
			ActivityWindowMonths: 6,
		},
		CoreRepositories: []string{"urbit/urbit", "urbit/vere"},
	}
	result := githubcollector.Result{
		Repositories: []githubcollector.RepositoryResult{
			{FullName: "urbit/urbit", Active: true, ActiveIdentities: identities(1, 2), AllTimeIdentities: identities(1, 2, 3)},
			{FullName: "urbit/vere", Active: true, ActiveIdentities: identities(2, 4), AllTimeIdentities: identities(2, 4, 5)},
			{FullName: "tocwex/fund", Active: true, ActiveIdentities: identities(6), AllTimeIdentities: identities(6, 7)},
		},
		ActiveRepositories: 3,
		ActiveIdentities:   identities(1, 2, 4, 6),
		AllTimeIdentities:  identities(1, 2, 3, 4, 5, 6, 7),
		Coverage: githubcollector.Coverage{
			Complete: true, CandidateRepositories: 8, IncludedRepositories: 3, Organizations: []string{"urbit", "tocwex"},
		},
	}
	snapshot, err := toSnapshot([]byte(`{"config":true}`), config, result, time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("toSnapshot() error = %v", err)
	}
	if got := *snapshot.Metrics.ActiveContributors.Count; got != 4 {
		t.Fatalf("active ecosystem contributors = %d, want 4", got)
	}
	if got := *snapshot.Metrics.AllTimeContributors.Count; got != 7 {
		t.Fatalf("all-time ecosystem contributors = %d, want 7", got)
	}
	if got := *snapshot.CoreRepositories["urbit/urbit"].ActiveContributors.Count; got != 2 {
		t.Fatalf("urbit/urbit active contributors = %d, want 2", got)
	}
}

func TestValidateOutputPathRejectsDataOverlap(t *testing.T) {
	root := t.TempDir()
	for _, output := range []string{filepath.Join(root, "data"), filepath.Join(root, "data", "site")} {
		if err := validateOutputPath(root, output); err == nil {
			t.Fatalf("validateOutputPath(%q) error = nil", output)
		}
	}
	for _, output := range []string{root, filepath.Dir(root)} {
		if err := validateOutputPath(root, output); err == nil {
			t.Fatalf("validateOutputPath(%q) error = nil for data-containing output", output)
		}
	}
	for _, output := range []string{filepath.Join(root, "site"), filepath.Join(root, "docs"), filepath.Join(root, "config"), filepath.Join(root, "custom-output")} {
		if err := validateOutputPath(root, output); err == nil {
			t.Fatalf("validateOutputPath(%q) error = nil for repository source path", output)
		}
	}
	if err := validateOutputPath(root, filepath.Join(root, "dist")); err != nil {
		t.Fatalf("validateOutputPath(dist) error = %v", err)
	}
}

func TestValidateOutputPathRejectsSymlinkedDataOverlap(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "repo-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	if err := validateOutputPath(root, filepath.Join(link, "data", "site")); err == nil {
		t.Fatal("validateOutputPath() error = nil for symlinked data descendant")
	}
}

func identities(ids ...int64) githubcollector.IdentitySet {
	set := make(githubcollector.IdentitySet, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

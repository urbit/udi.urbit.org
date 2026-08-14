package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	githubcollector "github.com/urbit/udi.urbit.org/internal/github"
)

const candidateReportSchemaVersion = 1

type candidateReport struct {
	SchemaVersion int                    `json:"schemaVersion"`
	GeneratedAt   string                 `json:"generatedAt"`
	Summary       candidateReportSummary `json:"summary"`
	Repositories  []candidateReportItem  `json:"repositories"`
}

type candidateReportSummary struct {
	Candidates int            `json:"candidates"`
	Included   int            `json:"included"`
	Decisions  map[string]int `json:"decisions"`
}

type candidateReportItem struct {
	ID              int64    `json:"id"`
	FullName        string   `json:"fullName"`
	URL             string   `json:"url"`
	Description     string   `json:"description,omitempty"`
	DefaultBranch   string   `json:"defaultBranch"`
	PrimaryLanguage string   `json:"primaryLanguage,omitempty"`
	PushedAt        string   `json:"pushedAt,omitempty"`
	Sources         []string `json:"sources"`
	Included        bool     `json:"included"`
	Decision        string   `json:"decision"`
	HasHoon         bool     `json:"hasHoon"`
	Active          *bool    `json:"active,omitempty"`
}

func newCandidateReport(candidates []githubcollector.CandidateRepository, coverage githubcollector.Coverage, generatedAt time.Time, activity map[string]bool) candidateReport {
	report := candidateReport{
		SchemaVersion: candidateReportSchemaVersion,
		GeneratedAt:   generatedAt.UTC().Format(time.RFC3339),
		Summary: candidateReportSummary{
			Candidates: coverage.CandidateRepositories,
			Included:   coverage.IncludedRepositories,
			Decisions:  make(map[string]int),
		},
		Repositories: make([]candidateReportItem, 0, len(candidates)),
	}
	for _, candidate := range candidates {
		pushedAt := ""
		if !candidate.PushedAt.IsZero() {
			pushedAt = candidate.PushedAt.UTC().Format(time.RFC3339)
		}
		var active *bool
		if value, ok := activity[strings.ToLower(candidate.FullName)]; ok {
			activeValue := value
			active = &activeValue
		}
		report.Repositories = append(report.Repositories, candidateReportItem{
			ID:              candidate.ID,
			FullName:        candidate.FullName,
			URL:             candidate.HTMLURL,
			Description:     candidate.Description,
			DefaultBranch:   candidate.DefaultBranch,
			PrimaryLanguage: candidate.PrimaryLanguage,
			PushedAt:        pushedAt,
			Sources:         append([]string(nil), candidate.Sources...),
			Included:        candidate.Included,
			Decision:        candidate.Decision,
			HasHoon:         candidate.HasHoon,
			Active:          active,
		})
		report.Summary.Decisions[candidate.Decision]++
	}
	sort.Slice(report.Repositories, func(i, j int) bool {
		return strings.ToLower(report.Repositories[i].FullName) < strings.ToLower(report.Repositories[j].FullName)
	})
	return report
}

func activityByRepository(repositories []githubcollector.RepositoryResult) map[string]bool {
	activity := make(map[string]bool, len(repositories))
	for _, repository := range repositories {
		activity[strings.ToLower(repository.FullName)] = repository.Active
	}
	return activity
}

func (report candidateReport) Validate() error {
	var problems []string
	if report.SchemaVersion != candidateReportSchemaVersion {
		problems = append(problems, "unsupported candidate report schema version")
	}
	if _, err := time.Parse(time.RFC3339, report.GeneratedAt); err != nil {
		problems = append(problems, "generatedAt must be RFC3339")
	}
	if report.Summary.Candidates != len(report.Repositories) {
		problems = append(problems, "candidate summary does not match repository list")
	}
	included := 0
	seenIDs := make(map[int64]bool, len(report.Repositories))
	seenNames := make(map[string]bool, len(report.Repositories))
	for _, repository := range report.Repositories {
		if repository.ID <= 0 || strings.TrimSpace(repository.FullName) == "" || strings.TrimSpace(repository.Decision) == "" {
			problems = append(problems, "candidate repository requires ID, fullName, and decision")
			continue
		}
		canonicalName := strings.ToLower(repository.FullName)
		if seenIDs[repository.ID] || seenNames[canonicalName] {
			problems = append(problems, "candidate repositories must be unique by ID and name")
		}
		seenIDs[repository.ID] = true
		seenNames[canonicalName] = true
		if repository.Included {
			included++
			if !strings.HasPrefix(repository.Decision, "included-") {
				problems = append(problems, "included candidate must have an included decision")
			}
		}
	}
	if included != report.Summary.Included {
		problems = append(problems, "included summary does not match repository list")
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func writeCandidateReport(path string, report candidateReport) error {
	if err := report.Validate(); err != nil {
		return fmt.Errorf("refuse to write invalid candidate report: %w", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode candidate report: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create candidate report directory %s: %w", filepath.Dir(path), err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".candidates-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary candidate report: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary candidate report: %w", err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return fmt.Errorf("set candidate report permissions: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary candidate report: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary candidate report: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace candidate report %s: %w", path, err)
	}
	return nil
}

func readCandidateActivity(path string) (map[string]bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read existing candidate report %s: %w", path, err)
	}
	var report candidateReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("decode existing candidate report %s: %w", path, err)
	}
	activity := make(map[string]bool)
	for _, repository := range report.Repositories {
		if repository.Active != nil {
			activity[strings.ToLower(repository.FullName)] = *repository.Active
		}
	}
	return activity, nil
}

func writeCandidateMarkdown(path string, report candidateReport) error {
	if err := report.Validate(); err != nil {
		return fmt.Errorf("refuse to write invalid candidate list: %w", err)
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "# UDI Repository Candidates\n\nGenerated: `%s`\n\n", report.GeneratedAt)
	fmt.Fprintf(&output, "- Candidates: **%d**\n- Included: **%d**\n\n", report.Summary.Candidates, report.Summary.Included)
	output.WriteString("Edit `config/repositories.json` to force a repository into `explicitInclude` or `explicitExclude`, then rerun discovery.\n\n")
	output.WriteString("| Included | Active | Repository | Decision | Last push | Sources |\n")
	output.WriteString("|---|---|---|---|---|---|\n")
	for _, repository := range report.Repositories {
		included := "no"
		if repository.Included {
			included = "yes"
		}
		active := "—"
		if repository.Active != nil {
			if *repository.Active {
				active = "yes"
			} else {
				active = "no"
			}
		}
		lastPush := repository.PushedAt
		if len(lastPush) >= 10 {
			lastPush = lastPush[:10]
		}
		name := strings.ReplaceAll(repository.FullName, "|", "\\|")
		descriptionSources := strings.ReplaceAll(strings.Join(repository.Sources, ", "), "|", "\\|")
		fmt.Fprintf(&output, "| %s | %s | [%s](%s) | `%s` | %s | %s |\n", included, active, name, repository.URL, repository.Decision, lastPush, descriptionSources)
	}
	return writeAtomicFile(path, output.Bytes())
}

func writeAtomicFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output directory %s: %w", filepath.Dir(path), err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".report-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary report: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary report: %w", err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		temporary.Close()
		return fmt.Errorf("set report permissions: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary report: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary report: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace report %s: %w", path, err)
	}
	return nil
}

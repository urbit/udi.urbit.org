package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	appconfig "github.com/urbit/udi.urbit.org/internal/config"
	githubcollector "github.com/urbit/udi.urbit.org/internal/github"
	"github.com/urbit/udi.urbit.org/internal/metrics"
	"github.com/urbit/udi.urbit.org/internal/site"
)

const usage = `Usage:
  go run ./cmd/udi build   [-root PATH] [-output PATH]
  go run ./cmd/udi discover [-root PATH] [-github-api URL]
  go run ./cmd/udi refresh [-root PATH] [-output PATH] [-github-api URL]

Commands:
  build    Render data/latest.json into the static output directory.
  discover Discover and classify candidate repositories without contributor collection.
  refresh  Collect GitHub metrics, validate and save the snapshot, then build.
`

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	if err := run(context.Background(), os.Args[1:]); err != nil {
		log.Printf("error: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch args[0] {
	case "build":
		return runBuild(args[1:])
	case "discover":
		return runDiscover(ctx, args[1:])
	case "refresh":
		return runRefresh(ctx, args[1:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n%s", args[0], usage)
	}
}

type paths struct {
	root   string
	output string
}

func commonFlags(name string, args []string) (paths, *flag.FlagSet, error) {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	root := set.String("root", ".", "repository root")
	output := set.String("output", "dist", "static output directory, relative to root unless absolute")
	if err := set.Parse(args); err != nil {
		return paths{}, set, err
	}
	if set.NArg() != 0 {
		return paths{}, set, fmt.Errorf("unexpected arguments: %s", strings.Join(set.Args(), " "))
	}
	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		return paths{}, set, fmt.Errorf("resolve repository root %s: %w", *root, err)
	}
	absoluteOutput := *output
	if !filepath.IsAbs(absoluteOutput) {
		absoluteOutput = filepath.Join(absoluteRoot, absoluteOutput)
	}
	return paths{root: absoluteRoot, output: absoluteOutput}, set, nil
}

func runBuild(args []string) error {
	resolved, _, err := commonFlags("build", args)
	if err != nil {
		return err
	}
	lock, err := acquireOperationLock(resolved.root, "build")
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			log.Printf("error: %v", releaseErr)
		}
	}()
	return build(resolved)
}

func build(resolved paths) error {
	if err := validateOutputPath(resolved.root, resolved.output); err != nil {
		return err
	}
	snapshotPath := filepath.Join(resolved.root, "data", "latest.json")
	snapshot, err := metrics.Read(snapshotPath)
	if err != nil {
		return err
	}
	log.Printf("building static site from %s (status=%s)", snapshotPath, snapshot.Status)
	if err := site.Build(resolved.root, resolved.output, snapshot); err != nil {
		return err
	}
	log.Printf("static site built at %s", resolved.output)
	return nil
}

func runRefresh(ctx context.Context, args []string) error {
	set := flag.NewFlagSet("refresh", flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	root := set.String("root", ".", "repository root")
	output := set.String("output", "dist", "static output directory, relative to root unless absolute")
	apiURL := set.String("github-api", "https://api.github.com", "GitHub REST API base URL")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(set.Args(), " "))
	}
	resolvedRoot, err := filepath.Abs(*root)
	if err != nil {
		return fmt.Errorf("resolve repository root %s: %w", *root, err)
	}
	resolvedOutput := *output
	if !filepath.IsAbs(resolvedOutput) {
		resolvedOutput = filepath.Join(resolvedRoot, resolvedOutput)
	}
	resolved := paths{root: resolvedRoot, output: resolvedOutput}
	if err := validateOutputPath(resolved.root, resolved.output); err != nil {
		return err
	}
	lock, err := acquireOperationLock(resolved.root, "refresh")
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			log.Printf("error: %v", releaseErr)
		}
	}()
	envPath := filepath.Join(resolved.root, ".env")
	if err := appconfig.LoadEnvFile(envPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	configPath := filepath.Join(resolved.root, "config", "repositories.json")
	configData, applicationConfig, err := readConfig(configPath)
	if err != nil {
		return err
	}
	config := applicationConfig.Config
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		return errors.New("GITHUB_TOKEN is required for refresh; no GitHub requests were made")
	}
	client, err := githubcollector.NewClient(githubcollector.ClientConfig{
		BaseURL: *apiURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	})
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Second)
	config.Now = func() time.Time { return now }
	config.ActiveUntil = now
	log.Printf("collecting GitHub data for %d organizations and %d explicit repositories", len(config.Organizations), len(config.ExplicitInclude))
	result, collectionErr := client.Collect(ctx, config)
	if collectionErr != nil || !result.Coverage.Complete {
		return fmt.Errorf("GitHub refresh incomplete; existing snapshot was not changed: %w", collectionErr)
	}
	candidateReport := newCandidateReport(result.Candidates, result.Coverage, now, activityByRepository(result.Repositories))
	if err := candidateReport.Validate(); err != nil {
		return fmt.Errorf("validate candidate report: %w", err)
	}
	snapshot, err := toSnapshot(configData, applicationConfig, result, now)
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(resolved.root, ".refresh-")
	if err != nil {
		return fmt.Errorf("create refresh staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	stagedData, err := stageDataDirectory(resolved.root, stage)
	if err != nil {
		return err
	}
	snapshotPath := filepath.Join(stagedData, "latest.json")
	historyPath := filepath.Join(stagedData, "history", now.Format("2006-01-02")+".json")
	if err := metrics.WriteAtomic(historyPath, snapshot); err != nil {
		return fmt.Errorf("write dated snapshot before publication: %w", err)
	}
	if err := writeCandidateReport(filepath.Join(stagedData, "candidates.json"), candidateReport); err != nil {
		return fmt.Errorf("write candidate report before snapshot publication: %w", err)
	}
	if err := writeCandidateMarkdown(filepath.Join(stagedData, "candidates.md"), candidateReport); err != nil {
		return fmt.Errorf("write candidate list before snapshot publication: %w", err)
	}
	if err := metrics.WriteAtomic(snapshotPath, snapshot); err != nil {
		return err
	}
	log.Printf("stored validated snapshot: candidates=%d included=%d active_repos=%d active_contributors=%d all_time_contributors=%d", result.Coverage.CandidateRepositories, result.Coverage.IncludedRepositories, result.ActiveRepositories, len(result.ActiveIdentities), len(result.AllTimeIdentities))
	if err := site.Build(resolved.root, filepath.Join(stage, "dist"), snapshot); err != nil {
		return fmt.Errorf("build refreshed site before publication: %w", err)
	}
	if err := publishDataAndSite(resolved.root, resolved.output, stage); err != nil {
		return err
	}
	log.Printf("static site built at %s", resolved.output)
	return nil
}

func validateOutputPath(root, output string) error {
	rootPath, err := canonicalPath(root)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	dataPath, err := canonicalPath(filepath.Join(root, "data"))
	if err != nil {
		return fmt.Errorf("resolve data path: %w", err)
	}
	outputPath, err := canonicalPath(output)
	if err != nil {
		return fmt.Errorf("resolve output path: %w", err)
	}
	relative, err := filepath.Rel(dataPath, outputPath)
	if err != nil {
		return fmt.Errorf("compare output path %s with data path %s: %w", outputPath, dataPath, err)
	}
	if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
		return fmt.Errorf("output path %s resolves inside the data directory %s", output, dataPath)
	}
	dataRelativeToOutput, err := filepath.Rel(outputPath, dataPath)
	if err != nil {
		return fmt.Errorf("compare data path %s with output path %s: %w", dataPath, outputPath, err)
	}
	if dataRelativeToOutput == "." || (dataRelativeToOutput != ".." && !strings.HasPrefix(dataRelativeToOutput, ".."+string(filepath.Separator))) {
		return fmt.Errorf("output path %s must not contain the data directory %s", output, dataPath)
	}
	outputRelativeToRoot, err := filepath.Rel(rootPath, outputPath)
	if err != nil {
		return fmt.Errorf("compare output path %s with repository root %s: %w", outputPath, rootPath, err)
	}
	insideRoot := outputRelativeToRoot == "." || (outputRelativeToRoot != ".." && !strings.HasPrefix(outputRelativeToRoot, ".."+string(filepath.Separator)))
	if insideRoot {
		allowedOutput := filepath.Join(rootPath, "dist")
		if outputPath != allowedOutput {
			return fmt.Errorf("output path inside the repository must be %s, got %s", allowedOutput, outputPath)
		}
	}
	return nil
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	cursor := filepath.Clean(absolute)
	var missing []string
	for {
		if _, err := os.Lstat(cursor); err == nil {
			resolved, err := filepath.EvalSymlinks(cursor)
			if err != nil {
				return "", err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(cursor)
		if parent == cursor {
			return "", fmt.Errorf("no existing ancestor for %s", path)
		}
		missing = append(missing, filepath.Base(cursor))
		cursor = parent
	}
}

func runDiscover(ctx context.Context, args []string) error {
	set := flag.NewFlagSet("discover", flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	root := set.String("root", ".", "repository root")
	apiURL := set.String("github-api", "https://api.github.com", "GitHub REST API base URL")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(set.Args(), " "))
	}
	resolvedRoot, err := filepath.Abs(*root)
	if err != nil {
		return fmt.Errorf("resolve repository root %s: %w", *root, err)
	}
	lock, err := acquireOperationLock(resolvedRoot, "discover")
	if err != nil {
		return err
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			log.Printf("error: %v", releaseErr)
		}
	}()
	if err := loadLocalEnv(resolvedRoot); err != nil {
		return err
	}
	_, applicationConfig, err := readConfig(filepath.Join(resolvedRoot, "config", "repositories.json"))
	if err != nil {
		return err
	}
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		return errors.New("GITHUB_TOKEN is required for discovery; no GitHub requests were made")
	}
	client, err := githubcollector.NewClient(githubcollector.ClientConfig{
		BaseURL: *apiURL,
		Token:   token,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	})
	if err != nil {
		return err
	}
	log.Printf("discovering GitHub repositories from %d organizations, %d searches, and %d explicit inclusions", len(applicationConfig.Organizations), len(applicationConfig.RepositorySearchQueries), len(applicationConfig.ExplicitInclude))
	result, discoveryErr := client.Discover(ctx, applicationConfig.Config)
	if discoveryErr != nil || !result.Coverage.Complete {
		return fmt.Errorf("GitHub discovery incomplete; existing candidate report was not changed: %w", discoveryErr)
	}
	existingCandidatePath := filepath.Join(resolvedRoot, "data", "candidates.json")
	previousActivity, err := readCandidateActivity(existingCandidatePath)
	if err != nil {
		return err
	}
	report := newCandidateReport(result.Candidates, result.Coverage, time.Now().UTC().Truncate(time.Second), previousActivity)
	stage, err := os.MkdirTemp(resolvedRoot, ".discover-")
	if err != nil {
		return fmt.Errorf("create discovery staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	stagedData, err := stageDataDirectory(resolvedRoot, stage)
	if err != nil {
		return err
	}
	path := filepath.Join(stagedData, "candidates.json")
	if err := writeCandidateReport(path, report); err != nil {
		return err
	}
	if err := writeCandidateMarkdown(filepath.Join(stagedData, "candidates.md"), report); err != nil {
		return err
	}
	if err := publishData(resolvedRoot, stage); err != nil {
		return err
	}
	log.Printf("stored candidate report at %s: candidates=%d included=%d", filepath.Join(resolvedRoot, "data", "candidates.json"), result.Coverage.CandidateRepositories, result.Coverage.IncludedRepositories)
	return nil
}

func loadLocalEnv(root string) error {
	envPath := filepath.Join(root, ".env")
	if err := appconfig.LoadEnvFile(envPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

type repositoryConfig struct {
	githubcollector.Config
	CoreRepositories []string `json:"coreRepositories"`
}

func readConfig(path string) ([]byte, repositoryConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, repositoryConfig{}, fmt.Errorf("read repository config %s: %w", path, err)
	}
	var config repositoryConfig
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return nil, repositoryConfig{}, fmt.Errorf("decode repository config %s: %w", path, err)
	}
	if config.ActivityWindowMonths != 6 {
		return nil, repositoryConfig{}, fmt.Errorf("repository config must use the approved six-month activity window, got %d", config.ActivityWindowMonths)
	}
	if len(config.Organizations) == 0 || len(config.ExplicitInclude) == 0 {
		return nil, repositoryConfig{}, errors.New("repository config requires organizations and explicit includes")
	}
	if len(config.CoreRepositories) != 2 {
		return nil, repositoryConfig{}, errors.New("repository config requires exactly two core repositories")
	}
	included := make(map[string]bool, len(config.ExplicitInclude))
	for _, name := range config.ExplicitInclude {
		included[strings.ToLower(name)] = true
	}
	for _, name := range config.CoreRepositories {
		if !included[strings.ToLower(name)] {
			return nil, repositoryConfig{}, fmt.Errorf("core repository %s must also be explicitly included", name)
		}
	}
	return data, config, nil
}

func toSnapshot(configData []byte, applicationConfig repositoryConfig, result githubcollector.Result, now time.Time) (metrics.Snapshot, error) {
	config := applicationConfig.Config
	activeSince := now.AddDate(0, -config.ActivityWindowMonths, 0)
	digest := sha256.Sum256(configData)
	snapshot := metrics.Snapshot{
		SchemaVersion:      metrics.SchemaVersion,
		MethodologyVersion: metrics.MethodologyVersion,
		ConfigDigest:       "sha256:" + hex.EncodeToString(digest[:]),
		Status:             metrics.StatusComplete,
		GeneratedAt:        now.Format(time.RFC3339),
		ActiveWindow: metrics.Window{
			Start: activeSince.Format(time.RFC3339),
			End:   now.Format(time.RFC3339),
			Label: "trailing six months",
		},
		Coverage: metrics.Coverage{
			Complete:              result.Coverage.Complete,
			CandidateRepositories: result.Coverage.CandidateRepositories,
			IncludedRepositories:  result.Coverage.IncludedRepositories,
			Organizations:         append([]string(nil), result.Coverage.Organizations...),
			Warnings:              append([]string(nil), result.Coverage.Warnings...),
		},
		Metrics: metrics.AggregateMetrics{
			ActiveRepositories:  metrics.Value{Count: metrics.Int(result.ActiveRepositories), Unit: "count"},
			ActiveContributors:  metrics.Value{Count: metrics.Int(len(result.ActiveIdentities)), Unit: "count"},
			AllTimeContributors: metrics.Value{Count: metrics.Int(len(result.AllTimeIdentities)), Unit: "count"},
		},
		CoreRepositories: make(map[string]metrics.RepositoryMetrics),
		Definitions:      metrics.Definitions(),
	}
	byName := make(map[string]githubcollector.RepositoryResult, len(result.Repositories))
	for _, repository := range result.Repositories {
		byName[strings.ToLower(repository.FullName)] = repository
	}
	core := append([]string(nil), applicationConfig.CoreRepositories...)
	sort.Strings(core)
	for _, name := range core {
		canonicalName := strings.ToLower(name)
		repository, ok := byName[canonicalName]
		if !ok {
			return metrics.Snapshot{}, fmt.Errorf("required core repository %s was not included; existing snapshot was not changed", name)
		}
		snapshot.CoreRepositories[canonicalName] = metrics.RepositoryMetrics{
			ActiveContributors:  metrics.Value{Count: metrics.Int(len(repository.ActiveIdentities)), Unit: "count"},
			AllTimeContributors: metrics.Value{Count: metrics.Int(len(repository.AllTimeIdentities)), Unit: "count"},
		}
	}
	if err := snapshot.Validate(); err != nil {
		return metrics.Snapshot{}, fmt.Errorf("validate collected snapshot: %w", err)
	}
	return snapshot, nil
}

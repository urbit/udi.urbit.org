// Package github collects repository and contributor activity from the GitHub
// REST API without exposing collection details to the metrics package.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.github.com/"
	perPage        = 100

	globalSearchDisabledWarning = "Global Hoon repository search is disabled because no repository search queries are configured."
)

// ClientConfig contains all process-specific GitHub client settings. The
// collector never reads configuration, credentials, or environment variables.
type ClientConfig struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// Config controls one collection run.
type Config struct {
	Organizations           []string `json:"organizations"`
	RepositorySearchQueries []string `json:"repositorySearchQueries"`
	ExplicitInclude         []string `json:"explicitInclude"`
	ExplicitExclude         []string `json:"explicitExclude"`
	ActivityWindowMonths    int      `json:"activityWindowMonths"`
	MaxPagesPerEndpoint     int      `json:"maxPagesPerEndpoint"`

	// ActiveSince overrides ActivityWindowMonths when non-zero. Now makes a
	// month-based window deterministic in tests; nil means time.Now.
	ActiveSince time.Time        `json:"-"`
	ActiveUntil time.Time        `json:"-"`
	Now         func() time.Time `json:"-"`
}

// IdentitySet is a set of linked GitHub numeric user IDs.
type IdentitySet map[int64]struct{}

// RepositoryResult contains the identity sets and activity state for one
// included repository.
type RepositoryResult struct {
	ID                int64
	FullName          string
	Active            bool
	ActiveIdentities  IdentitySet
	AllTimeIdentities IdentitySet
}

// CandidateRepository describes a discovered repository and its inclusion
// decision using public repository metadata only.
type CandidateRepository struct {
	ID              int64
	FullName        string
	HTMLURL         string
	Description     string
	DefaultBranch   string
	PrimaryLanguage string
	PushedAt        time.Time
	Sources         []string
	Included        bool
	Decision        string
	HasHoon         bool
}

// Coverage reports the scope and completeness of a collection run.
type Coverage struct {
	Complete              bool
	CandidateRepositories int
	IncludedRepositories  int
	Organizations         []string
	Warnings              []string
}

// DiscoveryResult contains the repository candidate audit and its coverage.
type DiscoveryResult struct {
	Candidates []CandidateRepository
	Coverage   Coverage
}

// Result contains per-repository and ecosystem-wide identity sets. Identities
// are numeric GitHub IDs only; no logins or other personal data are retained.
type Result struct {
	Repositories       []RepositoryResult
	Candidates         []CandidateRepository
	ActiveRepositories int
	ActiveIdentities   IdentitySet
	AllTimeIdentities  IdentitySet
	Coverage           Coverage
}

// Client is a GitHub REST API client.
type Client struct {
	baseURL    *url.URL
	token      string
	httpClient *http.Client
}

// NewClient constructs a client from passed configuration.
func NewClient(config ClientConfig) (*Client, error) {
	base := strings.TrimSpace(config.BaseURL)
	if base == "" {
		base = defaultBaseURL
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("parse GitHub base URL: %w", err)
	}
	if baseURL.Scheme != "http" && baseURL.Scheme != "https" {
		return nil, fmt.Errorf("parse GitHub base URL: unsupported scheme %q", baseURL.Scheme)
	}
	if baseURL.Host == "" {
		return nil, errors.New("parse GitHub base URL: host is required")
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{baseURL: baseURL, token: config.Token, httpClient: httpClient}, nil
}

type repository struct {
	ID              int64     `json:"id"`
	FullName        string    `json:"full_name"`
	HTMLURL         string    `json:"html_url"`
	Description     string    `json:"description"`
	DefaultBranch   string    `json:"default_branch"`
	PrimaryLanguage string    `json:"language"`
	Private         bool      `json:"private"`
	Fork            bool      `json:"fork"`
	Archived        bool      `json:"archived"`
	Disabled        bool      `json:"disabled"`
	PushedAt        time.Time `json:"pushed_at"`
}

type repositorySearchResult struct {
	TotalCount        int
	IncompleteResults bool
	Items             []repository
	pages             int
}

type discoveredRepository struct {
	repository repository
	sources    map[string]struct{}
}

type discoveryState struct {
	DiscoveryResult
	included []repository
}

type user struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Type  string `json:"type"`
}

type contributor struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Type  string `json:"type"`
}

type commit struct {
	Author *user `json:"author"`
}

type pullRequest struct {
	User     *user      `json:"user"`
	MergedAt *time.Time `json:"merged_at"`
}

// Discover finds and classifies repository candidates without collecting
// contributor or activity data.
func (c *Client) Discover(ctx context.Context, config Config) (DiscoveryResult, error) {
	discovery, err := c.discover(ctx, config)
	return discovery.DiscoveryResult, err
}

// Collect discovers repositories and collects their contributor activity. A
// non-nil error may accompany a useful partial Result.
func (c *Client) Collect(ctx context.Context, config Config) (Result, error) {
	result := Result{
		ActiveIdentities:  make(IdentitySet),
		AllTimeIdentities: make(IdentitySet),
		Coverage:          initialDiscoveryResult(config).Coverage,
	}
	activeSince, activeUntil, validationErrors := validateActivityConfig(config)
	if len(validationErrors) > 0 {
		result.Coverage.Complete = false
		return result, errors.Join(validationErrors...)
	}

	discovery, err := c.discover(ctx, config)
	result.Candidates = discovery.Candidates
	result.Coverage = discovery.Coverage
	if err != nil {
		return result, err
	}

	result.Coverage.IncludedRepositories = 0
	var collectionErrors []error
	for _, repo := range discovery.included {
		repositoryResult, errs := c.collectRepository(ctx, repo, activeSince, activeUntil, config.MaxPagesPerEndpoint)
		for _, err := range errs {
			collectionErrors = append(collectionErrors, err)
			result.Coverage.Complete = false
		}
		result.Repositories = append(result.Repositories, repositoryResult)
		if repositoryResult.Active {
			result.ActiveRepositories++
		}
		mergeIdentitySets(result.ActiveIdentities, repositoryResult.ActiveIdentities)
		mergeIdentitySets(result.AllTimeIdentities, repositoryResult.AllTimeIdentities)
		result.Coverage.IncludedRepositories = len(result.Repositories)
		if len(errs) > 0 {
			return result, errors.Join(collectionErrors...)
		}
	}

	return result, nil
}

func (c *Client) discover(ctx context.Context, config Config) (discoveryState, error) {
	result := discoveryState{DiscoveryResult: initialDiscoveryResult(config)}
	validationErrors := validateDiscoveryConfig(config)
	if len(validationErrors) > 0 {
		result.Coverage.Complete = false
		return result, errors.Join(validationErrors...)
	}
	if len(config.RepositorySearchQueries) > 0 && strings.TrimSpace(c.token) == "" {
		result.Coverage.Complete = false
		return result, errors.New("repository search requires an authenticated GitHub token")
	}

	exclusions := make(map[string]struct{}, len(config.ExplicitExclude))
	for _, name := range config.ExplicitExclude {
		exclusions[canonicalName(name)] = struct{}{}
	}

	repositories := make(map[int64]discoveredRepository)
	explicitIDs := make(map[int64]struct{})
	excludedIDs := make(map[int64]struct{})
	var discoveryErrors []error
	addError := func(err error) {
		discoveryErrors = append(discoveryErrors, err)
		result.Coverage.Complete = false
	}
	addRepository := func(repo repository, source string) {
		discovered, exists := repositories[repo.ID]
		if !exists {
			discovered.sources = make(map[string]struct{})
		}
		discovered.repository = repo
		discovered.sources[source] = struct{}{}
		repositories[repo.ID] = discovered
	}

	for _, organization := range config.Organizations {
		path := "orgs/" + url.PathEscape(organization) + "/repos?type=public&per_page=" + strconv.Itoa(perPage)
		var page []repository
		if err := c.getAll(ctx, path, config.MaxPagesPerEndpoint, &page); err != nil {
			addError(fmt.Errorf("enumerate organization %s repositories: %w", organization, err))
			return result, errors.Join(discoveryErrors...)
		}
		for _, repo := range page {
			if repo.Private {
				addError(fmt.Errorf("enumerate organization %s repository %s: private repository returned by public-only endpoint", organization, repo.FullName))
				return result, errors.Join(discoveryErrors...)
			}
			if err := validateRepository(repo); err != nil {
				addError(fmt.Errorf("enumerate organization %s repository: %w", organization, err))
				continue
			}
			addRepository(repo, "organization:"+organization)
		}
	}

	for _, query := range config.RepositorySearchQueries {
		parameters := url.Values{
			"q":        []string{query},
			"sort":     []string{"updated"},
			"order":    []string{"desc"},
			"per_page": []string{strconv.Itoa(perPage)},
		}
		var searchResult repositorySearchResult
		if err := c.getAll(ctx, "search/repositories?"+parameters.Encode(), config.MaxPagesPerEndpoint, &searchResult); err != nil {
			addError(fmt.Errorf("search repositories with query %q: %w", query, err))
			return result, errors.Join(discoveryErrors...)
		}
		if len(searchResult.Items) != searchResult.TotalCount {
			addError(fmt.Errorf("search repositories with query %q: repository search results are incomplete: expected %d items, received %d", query, searchResult.TotalCount, len(searchResult.Items)))
			return result, errors.Join(discoveryErrors...)
		}
		searchIDs := make(map[int64]string, len(searchResult.Items))
		for _, repo := range searchResult.Items {
			if firstName, duplicate := searchIDs[repo.ID]; duplicate {
				addError(fmt.Errorf("search repositories with query %q: repository search results are incomplete: duplicate repository ID %d for %q and %q", query, repo.ID, firstName, repo.FullName))
				return result, errors.Join(discoveryErrors...)
			}
			searchIDs[repo.ID] = repo.FullName
			if repo.Private {
				addError(fmt.Errorf("search repositories with query %q result %s: private repository returned by public-only endpoint", query, repo.FullName))
				return result, errors.Join(discoveryErrors...)
			}
			if err := validateRepository(repo); err != nil {
				addError(fmt.Errorf("search repositories with query %q result: %w", query, err))
				continue
			}
			addRepository(repo, "search:"+query)
		}
	}

	for _, name := range config.ExplicitInclude {
		var repo repository
		if err := c.get(ctx, "repos/"+escapeRepositoryName(name), &repo); err != nil {
			addError(fmt.Errorf("get explicitly included repository %s: %w", name, err))
			return result, errors.Join(discoveryErrors...)
		}
		if repo.Private {
			addError(fmt.Errorf("get explicitly included repository %s returned private repository %s: public-only discovery forbids private repositories", name, repo.FullName))
			return result, errors.Join(discoveryErrors...)
		}
		if err := validateRepository(repo); err != nil {
			addError(fmt.Errorf("get explicitly included repository %s: %w", name, err))
			continue
		}
		addRepository(repo, "explicit-include")
		explicitIDs[repo.ID] = struct{}{}
		if _, excluded := exclusions[canonicalName(name)]; excluded {
			excludedIDs[repo.ID] = struct{}{}
		}
	}

	ids := make([]int64, 0, len(repositories))
	for id := range repositories {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		left := repositories[ids[i]].repository.FullName
		right := repositories[ids[j]].repository.FullName
		leftFolded := strings.ToLower(left)
		rightFolded := strings.ToLower(right)
		if leftFolded != rightFolded {
			return leftFolded < rightFolded
		}
		if left != right {
			return left < right
		}
		return ids[i] < ids[j]
	})
	result.Candidates = make([]CandidateRepository, 0, len(ids))
	for _, id := range ids {
		discovered := repositories[id]
		sources := make([]string, 0, len(discovered.sources))
		for source := range discovered.sources {
			sources = append(sources, source)
		}
		sort.Strings(sources)
		repo := discovered.repository
		result.Candidates = append(result.Candidates, CandidateRepository{
			ID:              repo.ID,
			FullName:        repo.FullName,
			HTMLURL:         repo.HTMLURL,
			Description:     repo.Description,
			DefaultBranch:   repo.DefaultBranch,
			PrimaryLanguage: repo.PrimaryLanguage,
			PushedAt:        repo.PushedAt,
			Sources:         sources,
			HasHoon:         strings.EqualFold(repo.PrimaryLanguage, "Hoon"),
		})
	}
	result.Coverage.CandidateRepositories = len(result.Candidates)

	result.included = make([]repository, 0, len(ids))
	for index, id := range ids {
		repo := repositories[id].repository
		candidate := &result.Candidates[index]
		_, excludedByName := exclusions[canonicalName(repo.FullName)]
		_, excludedByID := excludedIDs[id]
		if excludedByName || excludedByID {
			candidate.Decision = "excluded-explicit"
			continue
		}
		if repo.Private {
			candidate.Decision = "excluded-private"
			continue
		}
		if repo.Fork {
			candidate.Decision = "excluded-fork"
			continue
		}
		if repo.Archived {
			candidate.Decision = "excluded-archived"
			continue
		}
		if repo.Disabled {
			candidate.Decision = "excluded-disabled"
			continue
		}
		if _, explicit := explicitIDs[id]; explicit {
			candidate.Included = true
			candidate.Decision = "included-explicit"
			result.included = append(result.included, repo)
			continue
		}
		if candidate.HasHoon {
			candidate.Included = true
			candidate.Decision = "included-hoon"
			result.included = append(result.included, repo)
			continue
		}
		var languages map[string]int64
		if err := c.get(ctx, "repos/"+escapeRepositoryName(repo.FullName)+"/languages", &languages); err != nil {
			addError(fmt.Errorf("get languages for repository %s: %w", repo.FullName, err))
			return result, errors.Join(discoveryErrors...)
		}
		candidate.HasHoon = hasHoonLanguage(languages)
		if candidate.HasHoon {
			candidate.Included = true
			candidate.Decision = "included-hoon"
			result.included = append(result.included, repo)
		} else {
			candidate.Decision = "excluded-no-hoon"
		}
	}
	result.Coverage.IncludedRepositories = len(result.included)

	return result, errors.Join(discoveryErrors...)
}

func initialDiscoveryResult(config Config) DiscoveryResult {
	warnings := []string(nil)
	if len(config.RepositorySearchQueries) == 0 {
		warnings = []string{globalSearchDisabledWarning}
	}
	return DiscoveryResult{
		Coverage: Coverage{
			Complete:      true,
			Organizations: append([]string(nil), config.Organizations...),
			Warnings:      warnings,
		},
	}
}

func validateDiscoveryConfig(config Config) []error {
	var errs []error
	for _, organization := range config.Organizations {
		if strings.TrimSpace(organization) == "" || strings.Contains(organization, "/") {
			errs = append(errs, fmt.Errorf("invalid organization %q", organization))
		}
	}
	for _, query := range config.RepositorySearchQueries {
		if strings.TrimSpace(query) == "" {
			errs = append(errs, fmt.Errorf("invalid repository search query %q: query cannot be empty", query))
		}
	}
	for field, names := range map[string][]string{
		"explicit include": config.ExplicitInclude,
		"explicit exclude": config.ExplicitExclude,
	} {
		for _, name := range names {
			if !validRepositoryName(name) {
				errs = append(errs, fmt.Errorf("invalid %s repository %q", field, name))
			}
		}
	}
	if config.MaxPagesPerEndpoint < 0 {
		errs = append(errs, errors.New("max pages per endpoint cannot be negative"))
	}
	return errs
}

func validateActivityConfig(config Config) (time.Time, time.Time, []error) {
	var errs []error
	activeSince := config.ActiveSince
	activeUntil := config.ActiveUntil
	now := time.Now
	if config.Now != nil {
		now = config.Now
	}
	if activeUntil.IsZero() {
		activeUntil = now().UTC()
	}
	if activeSince.IsZero() {
		if config.ActivityWindowMonths <= 0 {
			errs = append(errs, errors.New("active window requires ActiveSince or a positive ActivityWindowMonths"))
		} else {
			activeSince = activeUntil.UTC().AddDate(0, -config.ActivityWindowMonths, 0)
		}
	}
	if !activeSince.IsZero() && !activeSince.Before(activeUntil) {
		errs = append(errs, errors.New("active window start must precede active window end"))
	}
	return activeSince.UTC(), activeUntil.UTC(), errs
}

func hasHoonLanguage(languages map[string]int64) bool {
	for language, bytes := range languages {
		if bytes > 0 && strings.EqualFold(language, "Hoon") {
			return true
		}
	}
	return false
}

func validateRepository(repo repository) error {
	if repo.ID <= 0 {
		return fmt.Errorf("repository %q has no numeric ID", repo.FullName)
	}
	if !validRepositoryName(repo.FullName) {
		return fmt.Errorf("repository ID %d has invalid full name %q", repo.ID, repo.FullName)
	}
	return nil
}

func (c *Client) collectRepository(ctx context.Context, repo repository, activeSince, activeUntil time.Time, maxPages int) (RepositoryResult, []error) {
	result := RepositoryResult{
		ID:                repo.ID,
		FullName:          repo.FullName,
		ActiveIdentities:  make(IdentitySet),
		AllTimeIdentities: make(IdentitySet),
	}
	var errs []error
	basePath := "repos/" + escapeRepositoryName(repo.FullName)

	var contributors []contributor
	if err := c.getAll(ctx, basePath+"/contributors?anon=0&per_page="+strconv.Itoa(perPage), maxPages, &contributors); err != nil {
		errs = append(errs, fmt.Errorf("collect all-time contributors for repository %s: %w", repo.FullName, err))
	}
	for _, contributor := range contributors {
		addIdentity(result.AllTimeIdentities, contributor.ID, contributor.Login, contributor.Type)
	}

	commitPath := basePath + "/commits?per_page=" + strconv.Itoa(perPage) + "&sha=" + url.QueryEscape(repo.DefaultBranch) + "&since=" + url.QueryEscape(activeSince.Format(time.RFC3339)) + "&until=" + url.QueryEscape(activeUntil.Format(time.RFC3339))
	var commits []commit
	if err := c.getAll(ctx, commitPath, maxPages, &commits); err != nil {
		errs = append(errs, fmt.Errorf("collect active commits for repository %s: %w", repo.FullName, err))
	}
	if len(commits) > 0 {
		result.Active = true
	}
	for _, commit := range commits {
		if commit.Author == nil {
			continue
		}
		if addIdentity(result.ActiveIdentities, commit.Author.ID, commit.Author.Login, commit.Author.Type) {
			addIdentity(result.AllTimeIdentities, commit.Author.ID, commit.Author.Login, commit.Author.Type)
		}
	}

	var pullRequests []pullRequest
	if err := c.getAll(ctx, basePath+"/pulls?state=closed&per_page="+strconv.Itoa(perPage), maxPages, &pullRequests); err != nil {
		errs = append(errs, fmt.Errorf("collect closed pull requests for repository %s: %w", repo.FullName, err))
	}
	for _, pullRequest := range pullRequests {
		if pullRequest.MergedAt == nil {
			continue
		}
		if pullRequest.MergedAt.After(activeUntil) {
			continue
		}
		if pullRequest.User != nil {
			addIdentity(result.AllTimeIdentities, pullRequest.User.ID, pullRequest.User.Login, pullRequest.User.Type)
		}
		if pullRequest.MergedAt.Before(activeSince) {
			continue
		}
		result.Active = true
		if pullRequest.User != nil {
			addIdentity(result.ActiveIdentities, pullRequest.User.ID, pullRequest.User.Login, pullRequest.User.Type)
		}
	}

	return result, errs
}

func addIdentity(set IdentitySet, id int64, login, userType string) bool {
	if id <= 0 || isBot(login, userType) {
		return false
	}
	set[id] = struct{}{}
	return true
}

func isBot(login, userType string) bool {
	if strings.EqualFold(userType, "Bot") {
		return true
	}
	login = strings.ToLower(strings.TrimSpace(login))
	return strings.HasSuffix(login, "[bot]") || strings.HasSuffix(login, "-bot")
}

func mergeIdentitySets(destination, source IdentitySet) {
	for id := range source {
		destination[id] = struct{}{}
	}
}

func (c *Client) get(ctx context.Context, path string, destination any) error {
	requestURL, err := c.resolve(path)
	if err != nil {
		return err
	}
	return c.getURL(ctx, requestURL, destination)
}

func (c *Client) getAll(ctx context.Context, path string, maxPages int, destination any) error {
	requestURL, err := c.resolve(path)
	if err != nil {
		return err
	}
	page := 0
	visited := make(map[string]struct{})
	for requestURL != nil {
		page++
		if maxPages > 0 && page > maxPages {
			return fmt.Errorf("pagination exceeded configured maximum of %d pages", maxPages)
		}
		if _, exists := visited[requestURL.String()]; exists {
			return fmt.Errorf("pagination cycle at %s", requestURL.Redacted())
		}
		visited[requestURL.String()] = struct{}{}
		next, err := c.getPage(ctx, requestURL, destination)
		if err != nil {
			return fmt.Errorf("page %d: %w", page, err)
		}
		requestURL = next
	}
	return nil
}

func (c *Client) getURL(ctx context.Context, requestURL *url.URL, destination any) error {
	_, err := c.getPage(ctx, requestURL, destination)
	return err
}

func (c *Client) getPage(ctx context.Context, requestURL *url.URL, destination any) (*url.URL, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "udi.urbit.org-collector")
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", requestURL.Redacted(), err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("GET %s: status %s: %s", requestURL.Redacted(), response.Status, strings.TrimSpace(string(body)))
	}
	if response.StatusCode != http.StatusNoContent {
		if err := decodeAppend(response.Body, destination); err != nil {
			return nil, fmt.Errorf("decode GET %s: %w", requestURL.Redacted(), err)
		}
	}
	return c.nextPage(response.Header.Values("Link"))
}

func decodeAppend(reader io.Reader, destination any) error {
	decoder := json.NewDecoder(reader)
	switch value := destination.(type) {
	case *[]repository:
		var page []repository
		if err := decoder.Decode(&page); err != nil {
			return err
		}
		*value = append(*value, page...)
	case *[]contributor:
		var page []contributor
		if err := decoder.Decode(&page); err != nil {
			return err
		}
		*value = append(*value, page...)
	case *[]commit:
		var page []commit
		if err := decoder.Decode(&page); err != nil {
			return err
		}
		*value = append(*value, page...)
	case *[]pullRequest:
		var page []pullRequest
		if err := decoder.Decode(&page); err != nil {
			return err
		}
		*value = append(*value, page...)
	case *repositorySearchResult:
		var page struct {
			TotalCount        int          `json:"total_count"`
			IncompleteResults bool         `json:"incomplete_results"`
			Items             []repository `json:"items"`
		}
		if err := decoder.Decode(&page); err != nil {
			return err
		}
		if value.pages > 0 && page.TotalCount != value.TotalCount {
			return fmt.Errorf("repository search results are incomplete: total_count changed from %d to %d during pagination", value.TotalCount, page.TotalCount)
		}
		value.TotalCount = page.TotalCount
		value.IncompleteResults = value.IncompleteResults || page.IncompleteResults
		if page.IncompleteResults {
			return errors.New("repository search results are incomplete: GitHub reported incomplete_results=true")
		}
		if page.TotalCount > 1000 {
			return fmt.Errorf("repository search results are incomplete: total_count %d exceeds GitHub's 1,000-result cap", page.TotalCount)
		}
		value.Items = append(value.Items, page.Items...)
		value.pages++
	default:
		if err := decoder.Decode(destination); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) nextPage(headers []string) (*url.URL, error) {
	for _, header := range headers {
		for _, link := range strings.Split(header, ",") {
			parts := strings.Split(link, ";")
			if len(parts) < 2 {
				continue
			}
			isNext := false
			for _, parameter := range parts[1:] {
				if strings.TrimSpace(parameter) == `rel="next"` {
					isNext = true
					break
				}
			}
			if !isNext {
				continue
			}
			target := strings.TrimSpace(parts[0])
			if len(target) < 2 || target[0] != '<' || target[len(target)-1] != '>' {
				return nil, fmt.Errorf("parse next Link target %q", target)
			}
			next, err := url.Parse(target[1 : len(target)-1])
			if err != nil {
				return nil, fmt.Errorf("parse next Link URL: %w", err)
			}
			next = c.baseURL.ResolveReference(next)
			if !sameOrigin(c.baseURL, next) {
				return nil, fmt.Errorf("refuse cross-origin next Link URL %s", next.Redacted())
			}
			return next, nil
		}
	}
	return nil, nil
}

func (c *Client) resolve(path string) (*url.URL, error) {
	reference, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("parse GitHub API path: %w", err)
	}
	resolved := c.baseURL.ResolveReference(reference)
	if !sameOrigin(c.baseURL, resolved) {
		return nil, fmt.Errorf("refuse cross-origin GitHub API path %s", resolved.Redacted())
	}
	return resolved, nil
}

func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) && strings.EqualFold(left.Host, right.Host)
}

func validRepositoryName(name string) bool {
	parts := strings.Split(name, "/")
	return len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != ""
}

func canonicalName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func escapeRepositoryName(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) != 2 {
		return url.PathEscape(name)
	}
	return url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
}

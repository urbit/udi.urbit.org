package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCollectFiltersPaginatesAndAggregates(t *testing.T) {
	activeSince := time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC)
	activeUntil := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	activeMerge := "2026-03-01T12:00:00Z"
	oldMerge := "2025-01-01T12:00:00Z"
	futureMerge := "2026-09-01T12:00:00Z"

	var serverURL string
	var mu sync.Mutex
	requests := make(map[string]int)
	handler := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		mu.Lock()
		requests[request.URL.Path]++
		mu.Unlock()

		if got := request.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization header = %q, want Bearer test-token", got)
		}

		switch request.URL.Path {
		case "/orgs/urbit/repos":
			if request.URL.Query().Get("page") == "2" {
				writeJSON(response, `[
					{"id":3,"full_name":"urbit/vere","default_branch":"develop"},
					{"id":4,"full_name":"urbit/archive","default_branch":"main","archived":true}
				]`)
				return
			}
			response.Header().Set("Link", fmt.Sprintf(`<%s/orgs/urbit/repos?type=public&per_page=100&page=2>; rel="next", <%s/orgs/urbit/repos?type=public&per_page=100&page=2>; rel="last"`, serverURL, serverURL))
			writeJSON(response, `[
				{"id":1,"full_name":"urbit/hoon","default_branch":"main"},
				{"id":2,"full_name":"other/nohoon","default_branch":"main"}
			]`)
		case "/repos/urbit/vere":
			writeJSON(response, `{"id":3,"full_name":"urbit/vere","default_branch":"develop"}`)
		case "/repos/urbit/hoon/languages":
			writeJSON(response, `{"Hoon":1234,"Shell":10}`)
		case "/repos/other/nohoon/languages":
			writeJSON(response, `{"Go":1234}`)
		case "/repos/urbit/hoon/contributors":
			if got := request.URL.Query().Get("anon"); got != "0" {
				t.Errorf("contributors anon = %q, want 0", got)
			}
			if request.URL.Query().Get("page") == "2" {
				writeJSON(response, `[
					{"id":102,"login":"dependabot[bot]","type":"User"},
					{"id":103,"login":"release-bot","type":"User"},
					{"id":104,"login":"second-human","type":"User"}
				]`)
				return
			}
			response.Header().Set("Link", fmt.Sprintf(`<%s/repos/urbit/hoon/contributors?anon=0&per_page=100&page=2>; rel="next"`, serverURL))
			writeJSON(response, `[
				{"id":100,"login":"shared-human","type":"User"},
				{"id":101,"login":"actions","type":"Bot"}
			]`)
		case "/repos/urbit/vere/contributors":
			writeJSON(response, `[
				{"id":100,"login":"shared-human","type":"User"},
				{"id":200,"login":"vere-human","type":"User"}
			]`)
		case "/repos/urbit/hoon/commits":
			assertCommitQuery(t, request.URL.Query(), "main", activeSince, activeUntil)
			if request.URL.Query().Get("page") == "2" {
				writeJSON(response, `[
					{"author":{"id":101,"login":"actions","type":"Bot"}},
					{"author":{"id":105,"login":"active-human","type":"User"}}
				]`)
				return
			}
			response.Header().Add("Link", fmt.Sprintf(`<%s/repos/urbit/hoon/commits?per_page=100&sha=main&since=%s&until=%s&page=2>; rel="next"`, serverURL, url.QueryEscape(activeSince.Format(time.RFC3339)), url.QueryEscape(activeUntil.Format(time.RFC3339))))
			writeJSON(response, `[
				{"author":{"id":100,"login":"shared-human","type":"User"}},
				{"author":null}
			]`)
		case "/repos/urbit/vere/commits":
			assertCommitQuery(t, request.URL.Query(), "develop", activeSince, activeUntil)
			writeJSON(response, `[]`)
		case "/repos/urbit/hoon/pulls":
			if got := request.URL.Query().Get("state"); got != "closed" {
				t.Errorf("pull request state = %q, want closed", got)
			}
			if request.URL.Query().Get("page") == "2" {
				writeJSON(response, fmt.Sprintf(`[
					{"user":{"id":108,"login":"old-human","type":"User"},"merged_at":%q},
				{"user":{"id":109,"login":"merge[bot]","type":"User"},"merged_at":%q},
				{"user":{"id":110,"login":"future-human","type":"User"},"merged_at":%q}
			]`, oldMerge, activeMerge, futureMerge))
				return
			}
			response.Header().Set("Link", fmt.Sprintf(`<%s/repos/urbit/hoon/pulls?state=closed&per_page=100&page=2>; rel="next"`, serverURL))
			writeJSON(response, fmt.Sprintf(`[
				{"user":{"id":106,"login":"pr-human","type":"User"},"merged_at":%q},
				{"user":{"id":107,"login":"unmerged-human","type":"User"},"merged_at":null}
			]`, activeMerge))
		case "/repos/urbit/vere/pulls":
			writeJSON(response, fmt.Sprintf(`[
				{"user":{"id":200,"login":"vere-human","type":"User"},"merged_at":%q}
			]`, activeMerge))
		default:
			http.Error(response, "unexpected path "+request.URL.RequestURI(), http.StatusNotFound)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	serverURL = server.URL

	client, err := NewClient(ClientConfig{BaseURL: server.URL, Token: "test-token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.Collect(context.Background(), Config{
		Organizations:        []string{"urbit"},
		ExplicitInclude:      []string{"urbit/vere"},
		ActivityWindowMonths: 6,
		ActiveSince:          activeSince,
		ActiveUntil:          activeUntil,
		MaxPagesPerEndpoint:  10,
	})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if !result.Coverage.Complete {
		t.Fatal("Coverage.Complete = false, want true")
	}
	if result.Coverage.CandidateRepositories != 4 {
		t.Errorf("CandidateRepositories = %d, want 4", result.Coverage.CandidateRepositories)
	}
	if result.Coverage.IncludedRepositories != 2 {
		t.Errorf("IncludedRepositories = %d, want 2", result.Coverage.IncludedRepositories)
	}
	if len(result.Coverage.Warnings) != 1 || result.Coverage.Warnings[0] != globalSearchDisabledWarning {
		t.Errorf("Coverage.Warnings = %#v, want disabled global search warning", result.Coverage.Warnings)
	}
	if len(result.Candidates) != result.Coverage.CandidateRepositories {
		t.Errorf("len(Candidates) = %d, want coverage count %d", len(result.Candidates), result.Coverage.CandidateRepositories)
	}
	assertCandidate(t, result.Candidates, "other/nohoon", false, "excluded-no-hoon", false, "organization:urbit")
	assertCandidate(t, result.Candidates, "urbit/hoon", true, "included-hoon", true, "organization:urbit")
	assertCandidate(t, result.Candidates, "urbit/archive", false, "excluded-archived", false, "organization:urbit")
	assertCandidate(t, result.Candidates, "urbit/vere", true, "included-explicit", false, "explicit-include", "organization:urbit")
	if result.ActiveRepositories != 2 {
		t.Errorf("ActiveRepositories = %d, want 2", result.ActiveRepositories)
	}
	assertIdentitySet(t, "ecosystem active", result.ActiveIdentities, 100, 105, 106, 200)
	assertIdentitySet(t, "ecosystem all-time", result.AllTimeIdentities, 100, 104, 105, 106, 108, 200)

	if len(result.Repositories) != 2 {
		t.Fatalf("len(Repositories) = %d, want 2", len(result.Repositories))
	}
	byName := make(map[string]RepositoryResult)
	for _, repo := range result.Repositories {
		byName[repo.FullName] = repo
	}
	hoon, ok := byName["urbit/hoon"]
	if !ok {
		t.Fatal("Hoon repository was not included")
	}
	assertIdentitySet(t, "urbit/hoon active", hoon.ActiveIdentities, 100, 105, 106)
	assertIdentitySet(t, "urbit/hoon all-time", hoon.AllTimeIdentities, 100, 104, 105, 106, 108)
	vere, ok := byName["urbit/vere"]
	if !ok {
		t.Fatal("explicit urbit/vere repository was not included")
	}
	assertIdentitySet(t, "urbit/vere active", vere.ActiveIdentities, 200)
	assertIdentitySet(t, "urbit/vere all-time", vere.AllTimeIdentities, 100, 200)
	if _, ok := byName["other/nohoon"]; ok {
		t.Error("non-Hoon repository was included")
	}

	mu.Lock()
	defer mu.Unlock()
	for path, want := range map[string]int{
		"/orgs/urbit/repos":              2,
		"/repos/urbit/hoon/contributors": 2,
		"/repos/urbit/hoon/commits":      2,
		"/repos/urbit/hoon/pulls":        2,
	} {
		if requests[path] != want {
			t.Errorf("requests to %s = %d, want %d", path, requests[path], want)
		}
	}
	if requests["/repos/urbit/vere/languages"] != 0 {
		t.Errorf("explicit repository language requests = %d, want 0", requests["/repos/urbit/vere/languages"])
	}
}

func TestCollectReturnsPartialResultAndContextualErrors(t *testing.T) {
	activeSince := time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC)
	activeUntil := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/repos/urbit/vere":
			writeJSON(response, `{"id":3,"full_name":"urbit/vere","default_branch":"main"}`)
		case "/repos/urbit/vere/contributors":
			http.Error(response, "contributors unavailable", http.StatusBadGateway)
		case "/repos/urbit/vere/commits":
			writeJSON(response, `[{"author":{"id":200,"login":"active-human","type":"User"}}]`)
		case "/repos/urbit/vere/pulls":
			writeJSON(response, `[]`)
		default:
			http.Error(response, "unexpected path", http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.Collect(context.Background(), Config{
		Organizations:   nil,
		ExplicitInclude: []string{"urbit/vere"},
		ActiveSince:     activeSince,
		ActiveUntil:     activeUntil,
	})
	if err == nil {
		t.Fatal("Collect() error = nil, want partial collection errors")
	}
	for _, context := range []string{
		"collect all-time contributors for repository urbit/vere",
		"status 502 Bad Gateway",
	} {
		if !strings.Contains(err.Error(), context) {
			t.Errorf("Collect() error %q does not contain %q", err, context)
		}
	}
	if result.Coverage.Complete {
		t.Error("Coverage.Complete = true after required request failures")
	}
	if result.Coverage.IncludedRepositories != 1 || len(result.Repositories) != 1 {
		t.Fatalf("partial included repositories = %d/%d, want 1/1", result.Coverage.IncludedRepositories, len(result.Repositories))
	}
	if !result.Repositories[0].Active {
		t.Error("partial repository Active = false, want successful commit data retained")
	}
	assertIdentitySet(t, "partial active", result.ActiveIdentities, 200)
	assertIdentitySet(t, "partial all-time", result.AllTimeIdentities, 200)
}

func TestCollectRejectsIneligibleAndExcludedRepositories(t *testing.T) {
	var unexpected []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/orgs/urbit/repos":
			writeJSON(response, `[
				{"id":2,"full_name":"urbit/fork","default_branch":"main","fork":true},
				{"id":3,"full_name":"urbit/archive","default_branch":"main","archived":true},
				{"id":4,"full_name":"urbit/disabled","default_branch":"main","disabled":true},
				{"id":5,"full_name":"urbit/excluded","default_branch":"main"}
			]`)
		default:
			unexpected = append(unexpected, request.URL.RequestURI())
			http.Error(response, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.Collect(context.Background(), Config{
		Organizations:   []string{"urbit"},
		ExplicitExclude: []string{"URBIT/EXCLUDED"},
		ActiveSince:     time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC),
		ActiveUntil:     time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if len(unexpected) > 0 {
		t.Errorf("requests made for rejected repositories: %v", unexpected)
	}
	if result.Coverage.CandidateRepositories != 4 || result.Coverage.IncludedRepositories != 0 {
		t.Errorf("repository coverage = %d candidates/%d included, want 4/0", result.Coverage.CandidateRepositories, result.Coverage.IncludedRepositories)
	}
	assertCandidate(t, result.Candidates, "urbit/archive", false, "excluded-archived", false, "organization:urbit")
	assertCandidate(t, result.Candidates, "urbit/disabled", false, "excluded-disabled", false, "organization:urbit")
	assertCandidate(t, result.Candidates, "urbit/excluded", false, "excluded-explicit", false, "organization:urbit")
	assertCandidate(t, result.Candidates, "urbit/fork", false, "excluded-fork", false, "organization:urbit")
}

func TestDiscoverRejectsPrivateRepositoriesBeforeCandidateExport(t *testing.T) {
	tests := []struct {
		name       string
		config     Config
		token      string
		path       string
		response   string
		wantError  string
		wantSource string
	}{
		{
			name:       "organization",
			config:     Config{Organizations: []string{"urbit"}},
			path:       "/orgs/urbit/repos",
			response:   `[{"id":1,"full_name":"urbit/private","private":true}]`,
			wantError:  "private repository returned by public-only endpoint",
			wantSource: "enumerate organization urbit repository urbit/private",
		},
		{
			name:       "search",
			config:     Config{RepositorySearchQueries: []string{"is:public"}},
			token:      "token",
			path:       "/search/repositories",
			response:   `{"total_count":1,"incomplete_results":false,"items":[{"id":1,"full_name":"urbit/private","private":true}]}`,
			wantError:  "private repository returned by public-only endpoint",
			wantSource: `search repositories with query "is:public" result urbit/private`,
		},
		{
			name:       "explicit include",
			config:     Config{ExplicitInclude: []string{"urbit/private"}},
			path:       "/repos/urbit/private",
			response:   `{"id":1,"full_name":"urbit/private","private":true}`,
			wantError:  "public-only discovery forbids private repositories",
			wantSource: "get explicitly included repository urbit/private returned private repository urbit/private",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.path {
					http.Error(response, "unexpected path", http.StatusNotFound)
					return
				}
				writeJSON(response, test.response)
			}))
			defer server.Close()

			client, err := NewClient(ClientConfig{BaseURL: server.URL, Token: test.token, HTTPClient: server.Client()})
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			result, err := client.Discover(context.Background(), test.config)
			if err == nil || !strings.Contains(err.Error(), test.wantError) || !strings.Contains(err.Error(), test.wantSource) {
				t.Fatalf("Discover() error = %v, want containing %q and %q", err, test.wantError, test.wantSource)
			}
			if result.Coverage.Complete {
				t.Error("Coverage.Complete = true, want false")
			}
			if len(result.Candidates) != 0 {
				t.Errorf("Candidates = %#v, want no private candidate export", result.Candidates)
			}
		})
	}
}

func TestCollectExplicitExcludeWinsOverInclude(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/repos/old/name" {
			http.Error(response, "unexpected path", http.StatusNotFound)
			return
		}
		writeJSON(response, `{"id":1,"full_name":"new/name","default_branch":"main","language":"Hoon"}`)
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	config := testConfig()
	config.Organizations = nil
	config.ExplicitInclude = []string{"old/name"}
	config.ExplicitExclude = []string{"OLD/NAME"}
	result, err := client.Collect(context.Background(), config)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	assertCandidate(t, result.Candidates, "new/name", false, "excluded-explicit", true, "explicit-include")
	if result.Coverage.CandidateRepositories != 1 || result.Coverage.IncludedRepositories != 0 {
		t.Errorf("repository coverage = %d candidates/%d included, want 1/0", result.Coverage.CandidateRepositories, result.Coverage.IncludedRepositories)
	}
}

func TestCollectDetectsLowercaseHoonLanguage(t *testing.T) {
	var languageRequests int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/orgs/urbit/repos":
			writeJSON(response, `[{"id":1,"full_name":"urbit/lowercase","default_branch":"main"}]`)
		case "/repos/urbit/lowercase/languages":
			languageRequests++
			writeJSON(response, `{"hoon":42,"Shell":1}`)
		case "/repos/urbit/lowercase/contributors", "/repos/urbit/lowercase/commits", "/repos/urbit/lowercase/pulls":
			writeJSON(response, `[]`)
		default:
			http.Error(response, "unexpected path "+request.URL.RequestURI(), http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.Collect(context.Background(), testConfig())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	assertCandidate(t, result.Candidates, "urbit/lowercase", true, "included-hoon", true, "organization:urbit")
	if languageRequests != 1 {
		t.Errorf("language requests = %d, want 1", languageRequests)
	}
}

func TestCollectPrimaryLanguageHoonSkipsLanguagesRequest(t *testing.T) {
	var languageRequests int
	pushedAt := "2026-07-30T12:00:00Z"
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/orgs/urbit/repos":
			writeJSON(response, fmt.Sprintf(`[{"id":1,"full_name":"Urbit/Primary","html_url":"https://github.com/Urbit/Primary","description":"Primary Hoon repository","default_branch":"develop","language":"hoon","pushed_at":%q}]`, pushedAt))
		case "/repos/Urbit/Primary/languages":
			languageRequests++
			http.Error(response, "languages should not be requested", http.StatusInternalServerError)
		case "/repos/Urbit/Primary/contributors", "/repos/Urbit/Primary/commits", "/repos/Urbit/Primary/pulls":
			writeJSON(response, `[]`)
		default:
			http.Error(response, "unexpected path "+request.URL.RequestURI(), http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.Collect(context.Background(), testConfig())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	assertCandidate(t, result.Candidates, "Urbit/Primary", true, "included-hoon", true, "organization:urbit")
	candidate := result.Candidates[0]
	if candidate.HTMLURL != "https://github.com/Urbit/Primary" || candidate.Description != "Primary Hoon repository" || candidate.DefaultBranch != "develop" || candidate.PrimaryLanguage != "hoon" {
		t.Errorf("candidate metadata = %#v", candidate)
	}
	wantPushedAt, _ := time.Parse(time.RFC3339, pushedAt)
	if !candidate.PushedAt.Equal(wantPushedAt) {
		t.Errorf("candidate PushedAt = %v, want %v", candidate.PushedAt, wantPushedAt)
	}
	if languageRequests != 0 {
		t.Errorf("language requests = %d, want 0", languageRequests)
	}
}

func TestCollectSearchPaginatesDeduplicatesAndTracksSources(t *testing.T) {
	const query = "language:Hoon archived:false"
	var serverURL string
	var searchRequests int
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/orgs/urbit/repos":
			writeJSON(response, `[{"id":1,"full_name":"urbit/shared","default_branch":"main","language":"Hoon"}]`)
		case "/search/repositories":
			searchRequests++
			if request.Header.Get("Authorization") != "Bearer token" {
				t.Errorf("Authorization header = %q, want Bearer token", request.Header.Get("Authorization"))
			}
			if request.URL.Query().Get("q") != query || request.URL.Query().Get("sort") != "updated" || request.URL.Query().Get("order") != "desc" || request.URL.Query().Get("per_page") != "100" {
				t.Errorf("search query parameters = %v", request.URL.Query())
			}
			if request.URL.Query().Get("page") == "2" {
				writeJSON(response, `{"total_count":3,"incomplete_results":false,"items":[{"id":3,"full_name":"alpha/Second","default_branch":"main","language":"hoon"}]}`)
				return
			}
			response.Header().Set("Link", fmt.Sprintf(`<%s/search/repositories?q=%s&sort=updated&order=desc&per_page=100&page=2>; rel="next"`, serverURL, url.QueryEscape(query)))
			writeJSON(response, `{"total_count":3,"incomplete_results":false,"items":[{"id":1,"full_name":"urbit/shared","default_branch":"main","language":"hoon"},{"id":2,"full_name":"zeta/Explicit","default_branch":"main"}]}`)
		case "/repos/zeta/Explicit":
			writeJSON(response, `{"id":2,"full_name":"zeta/Explicit","default_branch":"main"}`)
		case "/repos/alpha/Second/contributors", "/repos/alpha/Second/commits", "/repos/alpha/Second/pulls",
			"/repos/urbit/shared/contributors", "/repos/urbit/shared/commits", "/repos/urbit/shared/pulls",
			"/repos/zeta/Explicit/contributors", "/repos/zeta/Explicit/commits", "/repos/zeta/Explicit/pulls":
			writeJSON(response, `[]`)
		default:
			http.Error(response, "unexpected path "+request.URL.RequestURI(), http.StatusNotFound)
		}
	}))
	defer server.Close()
	serverURL = server.URL

	client, err := NewClient(ClientConfig{BaseURL: server.URL, Token: "token", HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	config := testConfig()
	config.RepositorySearchQueries = []string{query}
	config.ExplicitInclude = []string{"zeta/Explicit"}
	result, err := client.Collect(context.Background(), config)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if searchRequests != 2 {
		t.Errorf("search requests = %d, want 2", searchRequests)
	}
	if len(result.Coverage.Warnings) != 0 {
		t.Errorf("Coverage.Warnings = %#v, want none", result.Coverage.Warnings)
	}
	if result.Coverage.CandidateRepositories != 3 || len(result.Candidates) != 3 || result.Coverage.IncludedRepositories != 3 {
		t.Errorf("coverage/candidates = %d/%d/%d, want 3/3/3", result.Coverage.CandidateRepositories, len(result.Candidates), result.Coverage.IncludedRepositories)
	}
	wantOrder := []string{"alpha/Second", "urbit/shared", "zeta/Explicit"}
	for index, want := range wantOrder {
		if result.Candidates[index].FullName != want {
			t.Errorf("Candidates[%d].FullName = %q, want %q", index, result.Candidates[index].FullName, want)
		}
	}
	assertCandidate(t, result.Candidates, "alpha/Second", true, "included-hoon", true, "search:"+query)
	assertCandidate(t, result.Candidates, "urbit/shared", true, "included-hoon", true, "organization:urbit", "search:"+query)
	assertCandidate(t, result.Candidates, "zeta/Explicit", true, "included-explicit", false, "explicit-include", "search:"+query)
}

func TestCollectRejectsIncompleteSearchResults(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "incomplete flag", body: `{"total_count":1,"incomplete_results":true,"items":[]}`, want: "incomplete_results=true"},
		{name: "result cap", body: `{"total_count":1001,"incomplete_results":false,"items":[]}`, want: "exceeds GitHub's 1,000-result cap"},
		{name: "missing rows", body: `{"total_count":2,"incomplete_results":false,"items":[{"id":1,"full_name":"urbit/one"}]}`, want: "expected 2 items, received 1"},
		{name: "duplicate ID", body: `{"total_count":2,"incomplete_results":false,"items":[{"id":1,"full_name":"urbit/one"},{"id":1,"full_name":"urbit/renamed"}]}`, want: `repository search results are incomplete: duplicate repository ID 1 for "urbit/one" and "urbit/renamed"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/search/repositories" {
					http.Error(response, "unexpected path", http.StatusNotFound)
					return
				}
				writeJSON(response, test.body)
			}))
			defer server.Close()

			client, err := NewClient(ClientConfig{BaseURL: server.URL, Token: "token", HTTPClient: server.Client()})
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			config := testConfig()
			config.Organizations = nil
			config.RepositorySearchQueries = []string{"language:Hoon"}
			result, err := client.Collect(context.Background(), config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Collect() error = %v, want containing %q", err, test.want)
			}
			if result.Coverage.Complete {
				t.Error("Coverage.Complete = true, want false")
			}
		})
	}
}

func TestCollectValidatesSearchQueriesAndAuthentication(t *testing.T) {
	client, err := NewClient(ClientConfig{BaseURL: "https://api.github.test"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	config := testConfig()
	config.Organizations = nil
	config.RepositorySearchQueries = []string{"  "}
	_, err = client.Collect(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "query cannot be empty") {
		t.Fatalf("empty query error = %v", err)
	}

	config.RepositorySearchQueries = []string{"language:Hoon"}
	_, err = client.Collect(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "authenticated GitHub token") {
		t.Fatalf("missing token error = %v", err)
	}
}

func TestDiscoverClassifiesWithoutActivityRequests(t *testing.T) {
	var activityRequests []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/contributors") || strings.HasSuffix(request.URL.Path, "/commits") || strings.HasSuffix(request.URL.Path, "/pulls") {
			activityRequests = append(activityRequests, request.URL.RequestURI())
			http.Error(response, "activity endpoint must not be called", http.StatusInternalServerError)
			return
		}
		switch request.URL.Path {
		case "/orgs/urbit/repos":
			writeJSON(response, `[{"id":1,"full_name":"urbit/lowercase","default_branch":"develop"}]`)
		case "/repos/urbit/lowercase/languages":
			writeJSON(response, `{"hoon":42}`)
		case "/repos/manual/include":
			writeJSON(response, `{"id":2,"full_name":"manual/include","default_branch":"trunk","language":"Go"}`)
		default:
			http.Error(response, "unexpected path "+request.URL.RequestURI(), http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.Discover(context.Background(), Config{
		Organizations:   []string{"urbit"},
		ExplicitInclude: []string{"manual/include"},
	})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(activityRequests) != 0 {
		t.Errorf("Discover() activity requests = %v, want none", activityRequests)
	}
	if result.Coverage.CandidateRepositories != 2 || result.Coverage.IncludedRepositories != 2 {
		t.Errorf("discovery coverage = %d candidates/%d included, want 2/2", result.Coverage.CandidateRepositories, result.Coverage.IncludedRepositories)
	}
	assertCandidate(t, result.Candidates, "manual/include", true, "included-explicit", false, "explicit-include")
	assertCandidate(t, result.Candidates, "urbit/lowercase", true, "included-hoon", true, "organization:urbit")
}

func TestDiscoverDoesNotRequireActivityWindow(t *testing.T) {
	client, err := NewClient(ClientConfig{BaseURL: "https://api.github.test"})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	result, err := client.Discover(context.Background(), Config{})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if !result.Coverage.Complete || result.Coverage.CandidateRepositories != 0 || result.Coverage.IncludedRepositories != 0 {
		t.Errorf("Discover() coverage = %#v, want complete empty discovery", result.Coverage)
	}
}

func testConfig() Config {
	return Config{
		Organizations: []string{"urbit"},
		ActiveSince:   time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC),
		ActiveUntil:   time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC),
	}
}

func assertCandidate(t *testing.T, candidates []CandidateRepository, fullName string, included bool, decision string, hasHoon bool, sources ...string) {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.FullName != fullName {
			continue
		}
		if candidate.Included != included || candidate.Decision != decision || candidate.HasHoon != hasHoon || !reflect.DeepEqual(candidate.Sources, sources) {
			t.Errorf("candidate %s = %#v, want included=%t decision=%q hasHoon=%t sources=%v", fullName, candidate, included, decision, hasHoon, sources)
		}
		return
	}
	t.Errorf("candidate %s not found in %#v", fullName, candidates)
}

func assertCommitQuery(t *testing.T, query url.Values, branch string, activeSince, activeUntil time.Time) {
	t.Helper()
	if got := query.Get("sha"); got != branch {
		t.Errorf("commit sha = %q, want %q", got, branch)
	}
	if got := query.Get("since"); got != activeSince.Format(time.RFC3339) {
		t.Errorf("commit since = %q, want %q", got, activeSince.Format(time.RFC3339))
	}
	if got := query.Get("until"); got != activeUntil.Format(time.RFC3339) {
		t.Errorf("commit until = %q, want %q", got, activeUntil.Format(time.RFC3339))
	}
}

func assertIdentitySet(t *testing.T, name string, got IdentitySet, want ...int64) {
	t.Helper()
	gotIDs := make([]int64, 0, len(got))
	for id := range got {
		gotIDs = append(gotIDs, id)
	}
	sort.Slice(gotIDs, func(i, j int) bool { return gotIDs[i] < gotIDs[j] })
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if !reflect.DeepEqual(gotIDs, want) {
		t.Errorf("%s identities = %v, want %v", name, gotIDs, want)
	}
}

func writeJSON(response http.ResponseWriter, body string) {
	response.Header().Set("Content-Type", "application/json")
	_, _ = response.Write([]byte(body))
}

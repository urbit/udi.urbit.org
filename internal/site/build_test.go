package site

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/urbit/udi.urbit.org/internal/metrics"
)

func TestBuildRendersDraftAndCopiesAssets(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "site", "index.html.tmpl"), `<p>{{formatCount .Snapshot.Metrics.ActiveContributors}}</p><p>{{.Freshness}}</p>{{if .IsDraft}}<p>draft</p>{{end}}`)
	write(t, filepath.Join(root, "site", "methodology.html.tmpl"), `<p>Methodology {{.Snapshot.MethodologyVersion}}</p>`)
	write(t, filepath.Join(root, "site", "styles.css"), `body{color:#3f3f3f}`)
	output := filepath.Join(root, "dist")
	if err := Build(root, output, metrics.DraftSnapshot()); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	page, err := os.ReadFile(filepath.Join(output, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<p>—</p>", "Awaiting first data refresh", "<p>draft</p>"} {
		if !strings.Contains(string(page), want) {
			t.Errorf("index.html missing %q: %s", want, page)
		}
	}
	if _, err := os.Stat(filepath.Join(output, "styles.css")); err != nil {
		t.Fatalf("copied styles: %v", err)
	}
	if _, err := os.Stat(filepath.Join(output, "data", "latest.json")); err != nil {
		t.Fatalf("copied data: %v", err)
	}
	methodology, err := os.ReadFile(filepath.Join(output, "methodology", "index.html"))
	if err != nil {
		t.Fatalf("rendered methodology: %v", err)
	}
	if !strings.Contains(string(methodology), "Methodology 2026-08-12.1") {
		t.Errorf("methodology page missing snapshot version: %s", methodology)
	}
}

func TestBuildRejectsInvalidSnapshotWithoutReplacingOutput(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "dist")
	write(t, filepath.Join(output, "sentinel.txt"), "keep")
	snapshot := metrics.DraftSnapshot()
	snapshot.SchemaVersion = 99
	if err := Build(root, output, snapshot); err == nil {
		t.Fatal("Build() error = nil, want validation error")
	}
	if data, err := os.ReadFile(filepath.Join(output, "sentinel.txt")); err != nil || string(data) != "keep" {
		t.Fatalf("existing output changed: data=%q err=%v", data, err)
	}
}

func TestProductionTemplatesRenderMetricReferences(t *testing.T) {
	root := filepath.Join("..", "..")
	output := filepath.Join(t.TempDir(), "dist")
	if err := Build(root, output, metrics.DraftSnapshot()); err != nil {
		t.Fatalf("Build() production template: %v", err)
	}
	page, err := os.ReadFile(filepath.Join(output, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<p class="methodology"><a href="https://network.urbit.org" target="_blank" rel="noopener noreferrer">For user metrics, visit network.urbit.org<span aria-hidden="true">↗</span></a></p>`,
		`<a href="/methodology/">Definitions and methodology`,
		`<a href="#reports">reports</a>`,
		`<div class="section__label"><span>03</span><h2 id="reports-title">Reports</h2></div>`,
		`<a href="https://urbit-development-institute.s3.us-east-2.amazonaws.com/Urbit+Security+Audit+-+June+17th.pdf" target="_blank" rel="noopener noreferrer">Urbit Constitution Security Audit`,
		`The audit found no high-severity issues`,
		`<div class="section__label"><span>04</span><h2 id="contribute-title">Contribute</h2></div>`,
	} {
		if !strings.Contains(string(page), want) {
			t.Errorf("index.html missing site content %q", want)
		}
	}
	methodology, err := os.ReadFile(filepath.Join(output, "methodology", "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"How the numbers are counted.", "Active contributor", "Methodology v2026-08-12.1"} {
		if !strings.Contains(string(methodology), want) {
			t.Errorf("methodology/index.html missing %q", want)
		}
	}
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

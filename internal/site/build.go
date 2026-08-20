// Package site renders the static UDI website from a validated aggregate snapshot.
package site

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/urbit/udi.urbit.org/internal/metrics"
)

// CoreRepository is a stable template-facing core repository row.
type CoreRepository struct {
	Name    string
	Metrics metrics.RepositoryMetrics
}

// PageData contains derived display values only.
type PageData struct {
	Snapshot         metrics.Snapshot
	CoreRepositories []CoreRepository
	IsDraft          bool
	Freshness        string
	FooterStatus     string
}

// Build validates the snapshot, renders index.html, and copies static assets.
func Build(root, output string, snapshot metrics.Snapshot) error {
	if err := snapshot.Validate(); err != nil {
		return fmt.Errorf("build site with invalid snapshot: %w", err)
	}
	source := filepath.Join(root, "site")
	temporary, err := os.MkdirTemp(filepath.Dir(output), ".udi-site-*")
	if err != nil {
		return fmt.Errorf("create temporary site output: %w", err)
	}
	defer os.RemoveAll(temporary)

	if err := copyStatic(source, temporary); err != nil {
		return err
	}
	data := pageData(snapshot)
	if err := renderPage(filepath.Join(source, "index.html.tmpl"), filepath.Join(temporary, "index.html"), data); err != nil {
		return err
	}
	if err := renderPage(filepath.Join(source, "methodology.html.tmpl"), filepath.Join(temporary, "methodology", "index.html"), data); err != nil {
		return err
	}
	if err := metrics.WriteAtomic(filepath.Join(temporary, "data", "latest.json"), snapshot); err != nil {
		return fmt.Errorf("write public metric snapshot: %w", err)
	}
	backup := output + ".previous"
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("remove stale site backup %s: %w", backup, err)
	}
	hadOutput := false
	if _, err := os.Stat(output); err == nil {
		hadOutput = true
		if err := os.Rename(output, backup); err != nil {
			return fmt.Errorf("stage existing site output %s: %w", output, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect existing site output %s: %w", output, err)
	}
	if err := os.Rename(temporary, output); err != nil {
		if hadOutput {
			if restoreErr := os.Rename(backup, output); restoreErr != nil {
				return fmt.Errorf("publish site output %s: %v; restore previous output: %w", output, err, restoreErr)
			}
		}
		return fmt.Errorf("publish site output %s: %w", output, err)
	}
	if err := os.RemoveAll(backup); err != nil {
		// Publication has already completed successfully. A stale backup is a
		// cleanup concern, not a failed build that should roll data back.
		fmt.Fprintf(os.Stderr, "warning: remove previous site output %s: %v\n", backup, err)
	}
	return nil
}

func pageData(snapshot metrics.Snapshot) PageData {
	repositories := make([]CoreRepository, 0, len(snapshot.CoreRepositories))
	for name, repositoryMetrics := range snapshot.CoreRepositories {
		repositories = append(repositories, CoreRepository{Name: name, Metrics: repositoryMetrics})
	}
	sort.Slice(repositories, func(i, j int) bool { return repositories[i].Name < repositories[j].Name })
	data := PageData{Snapshot: snapshot, CoreRepositories: repositories, IsDraft: snapshot.Status == metrics.StatusDraft}
	if data.IsDraft {
		data.Freshness = "Awaiting first data refresh"
		data.FooterStatus = "Data draft / methodology v" + snapshot.MethodologyVersion
		return data
	}
	measuredAt, err := time.Parse(time.RFC3339, snapshot.GeneratedAt)
	if err == nil {
		data.Freshness = "Updated " + measuredAt.UTC().Format("02 Jan 2006")
	} else {
		data.Freshness = "Updated " + snapshot.GeneratedAt
	}
	data.FooterStatus = data.Freshness + " / methodology v" + snapshot.MethodologyVersion
	return data
}

func renderPage(templatePath, outputPath string, data PageData) error {
	page, err := template.New(filepath.Base(templatePath)).Funcs(template.FuncMap{
		"formatCount": func(value metrics.Value) string {
			if value.Count == nil {
				return "—"
			}
			return fmt.Sprintf("%d", *value.Count)
		},
	}).ParseFiles(templatePath)
	if err != nil {
		return fmt.Errorf("parse site template %s: %w", templatePath, err)
	}
	file, err := createFile(outputPath)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := page.Execute(file, data); err != nil {
		return fmt.Errorf("render site template %s: %w", templatePath, err)
	}
	return nil
}

func copyStatic(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk site source %s: %w", path, walkErr)
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return fmt.Errorf("resolve site source path %s: %w", path, err)
		}
		if relative == "." || strings.HasSuffix(entry.Name(), ".tmpl") {
			return nil
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create site asset directory %s: %w", target, err)
			}
			return nil
		}
		if err := copyFile(path, target); err != nil {
			return fmt.Errorf("copy site asset %s: %w", path, err)
		}
		return nil
	})
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := createFile(destination)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		output.Close()
		return err
	}
	return output.Close()
}

func createFile(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create directory %s: %w", filepath.Dir(path), err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, fmt.Errorf("create file %s: %w", path, err)
	}
	return file, nil
}

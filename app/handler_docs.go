package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// DocsHandler serves documentation files from the tai-golang/docs/ directory.
type DocsHandler struct{}

// docsDir locates the SDK documentation directory.
//
// The candidates are anchored to this program rather than to the working
// directory: a Tai node checkout often sits next to tai-golang, so a plain
// "docs" candidate resolves to that other repository's documentation and the
// console silently shows the wrong manual. A directory only qualifies when it
// contains docs.json, which marks it as the SDK's own doc tree.
func docsDir() string {
	var candidates []string

	// Alongside the executable: dist/tai-demo -> ../docs, or app/tai-demo -> ../docs.
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(dir, "..", "docs"),
			filepath.Join(dir, "docs"),
		)
	}

	// Source layout: app/ -> ../docs. Covers `go run .` from tai-golang/app.
	if wd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(wd, "..", "docs"),
			filepath.Join(wd, "docs"),
		)
	}

	for _, c := range candidates {
		if isSDKDocsDir(c) {
			return c
		}
	}
	return "../docs"
}

// isSDKDocsDir reports whether dir holds the SDK doc tree. docs.json is the
// marker: it is the index the console renders, and only this repository ships
// one alongside the SDK markdown files.
func isSDKDocsDir(dir string) bool {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return false
	}
	if _, err := os.Stat(filepath.Join(dir, "docs.json")); err != nil {
		return false
	}
	return true
}

// docEntry represents a documentation file with its display title.
type docEntry struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

// loadDocOrder reads docs.json for the ordered list of documentation files.
// Returns nil if the file is missing or invalid.
func loadDocOrder(dir string) []docEntry {
	data, err := os.ReadFile(filepath.Join(dir, "docs.json"))
	if err != nil {
		return nil
	}
	var entries []docEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil
	}
	return entries
}

// List returns all .md files in the docs directory, ordered by docs.json.
// Files not listed in docs.json are appended alphabetically.
func (h *DocsHandler) List(w http.ResponseWriter, _ *http.Request) {
	dir := docsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, []any{})
		return
	}

	// Index all .md files by name.
	fileSet := make(map[string]bool)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			fileSet[e.Name()] = true
		}
	}

	var docs []docEntry

	// Use docs.json order if available.
	if order := loadDocOrder(dir); len(order) > 0 {
		for _, entry := range order {
			if fileSet[entry.Name] {
				title := entry.Title
				if title == "" {
					title = extractTitle(filepath.Join(dir, entry.Name))
				}
				if title == "" {
					title = strings.TrimSuffix(entry.Name, ".md")
				}
				docs = append(docs, docEntry{Name: entry.Name, Title: title})
				delete(fileSet, entry.Name)
			}
		}
	}

	// Append remaining .md files not in docs.json.
	for _, e := range entries {
		if fileSet[e.Name()] {
			title := extractTitle(filepath.Join(dir, e.Name()))
			if title == "" {
				title = strings.TrimSuffix(e.Name(), ".md")
			}
			docs = append(docs, docEntry{Name: e.Name(), Title: title})
		}
	}
	writeJSON(w, docs)
}

// Get returns the raw content of a specific .md file.
func (h *DocsHandler) Get(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !strings.HasSuffix(name, ".md") {
		name += ".md"
	}
	// Prevent path traversal.
	if strings.Contains(name, "..") || strings.Contains(name, "/") {
		writeError(w, http.StatusBadRequest, "invalid name")
		return
	}

	data, err := os.ReadFile(filepath.Join(docsDir(), name))
	if err != nil {
		writeError(w, http.StatusNotFound, "document not found")
		return
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Write(data)
}

// extractTitle reads the first H1 heading from a markdown file.
func extractTitle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "# ") {
			return strings.TrimPrefix(line, "# ")
		}
	}
	return ""
}

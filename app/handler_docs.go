package main

import (
	"bufio"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// DocsHandler serves documentation files from the tai-golang/docs/ directory.
type DocsHandler struct{}

func docsDir() string {
	// Resolve relative to the binary's working directory.
	// In development: go run . runs from tai-golang/app/, so docs is at ../docs/.
	candidates := []string{"../docs", "docs"}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return "../docs"
}

// docOrder defines the reading order for documentation files.
// Files not in this list are appended at the end alphabetically.
var docOrder = []string{
	"index.md",
	"quickstart.md",
	"architecture.md",
	"api-reference.md",
	"session-protocol.md",
	"events.md",
	"uri.md",
	"demo-guide.md",
}

// List returns all .md files in the docs directory, ordered by reading sequence.
func (h *DocsHandler) List(w http.ResponseWriter, _ *http.Request) {
	dir := docsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, []any{})
		return
	}

	type docEntry struct {
		Name  string `json:"name"`
		Title string `json:"title"`
	}

	// Index all .md files by name.
	fileSet := make(map[string]bool)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			fileSet[e.Name()] = true
		}
	}

	// Ordered list: known files first, then remaining alphabetically.
	var docs []docEntry
	for _, name := range docOrder {
		if fileSet[name] {
			title := extractTitle(filepath.Join(dir, name))
			if title == "" {
				title = strings.TrimSuffix(name, ".md")
			}
			docs = append(docs, docEntry{Name: name, Title: title})
			delete(fileSet, name)
		}
	}
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

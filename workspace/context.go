package workspace

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/yaoapp/tai-golang/types"
)

// ContextScope defines the scope level for context directories.
type ContextScope string

const (
	ScopeBot     ContextScope = "bot"     // runtime instance defaults
	ScopeHome    ContextScope = "home"    // user-level preferences
	ScopeBoard   ContextScope = "board"   // board/project shared context
	ScopeSession ContextScope = "session" // single session context
)

// ContextEntry identifies a scope instance participating in a merge.
type ContextEntry struct {
	Scope ContextScope
	ID    string // e.g. botID, userID, boardID, sessionID
}

// ContextFiles holds the standard files read from a scope directory.
type ContextFiles struct {
	Instruction []byte // INSTRUCTION.md content (nil if absent)
	Memory      []byte // MEMORY.md content (nil if absent)
	Preference  []byte // PREFERENCE.md content (nil if absent)
}

// Empty reports whether all fields are nil.
func (cf *ContextFiles) Empty() bool {
	return cf.Instruction == nil && cf.Memory == nil && cf.Preference == nil
}

// FormatSection formats the non-nil files as a Markdown section with the given label.
func (cf *ContextFiles) FormatSection(label string) string {
	var parts []string
	if cf.Instruction != nil {
		parts = append(parts, "### INSTRUCTION.md\n\n"+strings.TrimSpace(string(cf.Instruction)))
	}
	if cf.Memory != nil {
		parts = append(parts, "### MEMORY.md\n\n"+strings.TrimSpace(string(cf.Memory)))
	}
	if cf.Preference != nil {
		parts = append(parts, "### PREFERENCE.md\n\n"+strings.TrimSpace(string(cf.Preference)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "## " + label + "\n\n" + strings.Join(parts, "\n\n")
}

// ContextDir returns the standard directory path for a given scope and ID.
// Example: ContextDir(ScopeBoard, "board-42") returns ".context/board/board-42".
func ContextDir(scope ContextScope, id string) string {
	return ".context/" + string(scope) + "/" + id
}

// SeedContext writes default files into a scope directory, only if they do not
// already exist. Used to seed bot defaults on first run without overwriting
// files the agent may have updated.
//
// The files map keys are filenames (e.g. "INSTRUCTION.md"), values are content.
func SeedContext(fsys types.WorkspaceFS, scope ContextScope, id string, files map[string][]byte) error {
	dir := ContextDir(scope, id)
	if err := fsys.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("seed context mkdir %s: %w", dir, err)
	}

	for name, content := range files {
		path := dir + "/" + name
		_, err := fs.Stat(fsys, path)
		if err == nil {
			continue // file exists, do not overwrite
		}
		w, err := fsys.Create(path)
		if err != nil {
			return fmt.Errorf("seed context create %s: %w", path, err)
		}
		if _, err := w.Write(content); err != nil {
			w.Close()
			return fmt.Errorf("seed context write %s: %w", path, err)
		}
		if err := w.Close(); err != nil {
			return fmt.Errorf("seed context close %s: %w", path, err)
		}
	}
	return nil
}

// ReadContext reads the standard files from a scope directory.
// Missing files result in nil fields, not errors.
func ReadContext(fsys fs.FS, scope ContextScope, id string) (*ContextFiles, error) {
	dir := ContextDir(scope, id)
	cf := &ContextFiles{}

	cf.Instruction = readFileOrNil(fsys, dir+"/INSTRUCTION.md")
	cf.Memory = readFileOrNil(fsys, dir+"/MEMORY.md")
	cf.Preference = readFileOrNil(fsys, dir+"/PREFERENCE.md")

	return cf, nil
}

// MergeContext reads multiple scope directories in order and merges them into
// a single Markdown string. Entries are ordered from lowest to highest priority
// (typically bot → home → board → session). The result can be injected into
// AGENTS.md.
func MergeContext(fsys fs.FS, entries []ContextEntry) (string, error) {
	var sections []string
	for _, e := range entries {
		files, err := ReadContext(fsys, e.Scope, e.ID)
		if err != nil {
			return "", err
		}
		if files.Empty() {
			continue
		}
		label := string(e.Scope) + "/" + e.ID
		sections = append(sections, files.FormatSection(label))
	}
	return strings.Join(sections, "\n\n"), nil
}

// readFileOrNil reads a file, returning nil if it does not exist.
// readFileOrNil reads a file, returning nil if it does not exist.
//
// A remote Open failure returns *ErrorResponse (Code "OPEN_FAILED") rather than
// fs.ErrNotExist. Treating any read error as "absent" keeps ReadContext callers
// independent of whether the filesystem is local or remote.
func readFileOrNil(fsys fs.FS, path string) []byte {
	data, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil
	}
	return data
}

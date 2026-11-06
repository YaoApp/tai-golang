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

// contextFileNames are the only filenames SeedContext writes. Any other key is
// rejected: the map arrives from callers and would let a key such as
// "../escape.md" write outside the scope directory.
var contextFileNames = map[string]struct{}{
	"INSTRUCTION.md": {},
	"MEMORY.md":      {},
	"PREFERENCE.md":  {},
}

// validContextScope reports whether scope is one of the four defined scopes.
//
// @param scope candidate scope
// @returns true for bot, home, board and session
func validContextScope(scope ContextScope) bool {
	switch scope {
	case ScopeBot, ScopeHome, ScopeBoard, ScopeSession:
		return true
	}
	return false
}

// validContextID reports whether id can name a scope directory. The id reaches
// the filesystem as a path segment, so separators and parent references are
// rejected; empty and "." would also collide with the scope directory itself.
//
// @param id candidate scope identifier
// @returns true when id is a single safe path segment
func validContextID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	return !strings.ContainsAny(id, `/\`)
}

// validateContextEntry reports the first invalid part of a scope entry.
//
// @param scope candidate scope
// @param id candidate scope identifier
// @returns an error naming the rejected value, or nil
func validateContextEntry(scope ContextScope, id string) error {
	if !validContextScope(scope) {
		return fmt.Errorf("context: unknown scope %q", scope)
	}
	if !validContextID(id) {
		return fmt.Errorf("context: invalid id %q", id)
	}
	return nil
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
//
// The result is a path only; SeedContext, ReadContext and MergeContext validate
// scope and id before touching the filesystem.
func ContextDir(scope ContextScope, id string) string {
	return ".context/" + string(scope) + "/" + id
}

// SeedContext writes default files into a scope directory, only if they do not
// already exist. Used to seed bot defaults on first run without overwriting
// files the agent may have updated.
//
// The files map keys are filenames ("INSTRUCTION.md", "MEMORY.md" or
// "PREFERENCE.md"); any other key is an error. Unknown scopes and ids that are
// not a single path segment are rejected before the filesystem is touched.
func SeedContext(fsys types.WorkspaceFS, scope ContextScope, id string, files map[string][]byte) error {
	if err := validateContextEntry(scope, id); err != nil {
		return err
	}
	for name := range files {
		if _, ok := contextFileNames[name]; !ok {
			return fmt.Errorf("seed context: unknown filename %q", name)
		}
	}

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
// Missing files result in nil fields, not errors. Unknown scopes and ids that
// are not a single path segment are rejected.
func ReadContext(fsys fs.FS, scope ContextScope, id string) (*ContextFiles, error) {
	if err := validateContextEntry(scope, id); err != nil {
		return nil, err
	}
	return readContextFiles(fsys, scope, id), nil
}

// MergeContext reads multiple scope directories in order and merges them into
// a single Markdown string. Entries are ordered from lowest to highest priority
// (typically bot → home → board → session). The result can be injected into
// AGENTS.md.
//
// Every entry is validated before any file is read, so one bad entry fails the
// whole merge instead of silently skipping its scope. Read failures are not
// possible after validation: a missing file reads as absent.
func MergeContext(fsys fs.FS, entries []ContextEntry) (string, error) {
	for _, e := range entries {
		if err := validateContextEntry(e.Scope, e.ID); err != nil {
			return "", err
		}
	}

	var sections []string
	for _, e := range entries {
		files := readContextFiles(fsys, e.Scope, e.ID)
		if files.Empty() {
			continue
		}
		label := string(e.Scope) + "/" + e.ID
		sections = append(sections, files.FormatSection(label))
	}
	return strings.Join(sections, "\n\n"), nil
}

// readContextFiles reads a scope directory whose entry is already validated.
//
// @param fsys filesystem to read from
// @param scope validated scope
// @param id validated scope identifier
// @returns the files present in the scope, with absent files as nil fields
func readContextFiles(fsys fs.FS, scope ContextScope, id string) *ContextFiles {
	dir := ContextDir(scope, id)
	return &ContextFiles{
		Instruction: readFileOrNil(fsys, dir+"/INSTRUCTION.md"),
		Memory:      readFileOrNil(fsys, dir+"/MEMORY.md"),
		Preference:  readFileOrNil(fsys, dir+"/PREFERENCE.md"),
	}
}

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

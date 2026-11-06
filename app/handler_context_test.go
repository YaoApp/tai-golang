package main

import (
	"testing"

	"github.com/yaoapp/tai-golang/workspace"
)

func TestParseScopeIDs(t *testing.T) {
	cases := []struct {
		raw  string
		want map[workspace.ContextScope]string
	}{
		{"", map[workspace.ContextScope]string{}},
		{"bot:b1", map[workspace.ContextScope]string{workspace.ScopeBot: "b1"}},
		{
			"bot:b1,home:h1,board:bd1,session:s1",
			map[workspace.ContextScope]string{
				workspace.ScopeBot: "b1", workspace.ScopeHome: "h1",
				workspace.ScopeBoard: "bd1", workspace.ScopeSession: "s1",
			},
		},
		// Empty value is dropped.
		{"bot:,home:h1", map[workspace.ContextScope]string{workspace.ScopeHome: "h1"}},
		// Entry without a colon is dropped.
		{"bot,home:h1", map[workspace.ContextScope]string{workspace.ScopeHome: "h1"}},
		// Only the first colon splits, so IDs may contain colons.
		{"session:a:b", map[workspace.ContextScope]string{workspace.ScopeSession: "a:b"}},
	}
	for _, c := range cases {
		got := parseScopeIDs(c.raw)
		if len(got) != len(c.want) {
			t.Errorf("parseScopeIDs(%q) = %v, want %v", c.raw, got, c.want)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("parseScopeIDs(%q)[%s] = %q, want %q", c.raw, k, got[k], v)
			}
		}
	}
}

func TestValidScope(t *testing.T) {
	valid := []workspace.ContextScope{
		workspace.ScopeBot, workspace.ScopeHome,
		workspace.ScopeBoard, workspace.ScopeSession,
	}
	for _, s := range valid {
		if !validScope(s) {
			t.Errorf("%q should be a valid scope", s)
		}
	}
	invalid := []workspace.ContextScope{"", "user", "BOT", "bot "}
	for _, s := range invalid {
		if validScope(s) {
			t.Errorf("%q should not be a valid scope", s)
		}
	}
}

// Scope IDs are concatenated into a filesystem path, so traversal sequences and
// separators must be rejected.
func TestValidScopeID(t *testing.T) {
	valid := []string{"demo", "bot-1", "user_42", "abc123"}
	for _, id := range valid {
		if !validScopeID(id) {
			t.Errorf("%q should be a valid scope ID", id)
		}
	}
	invalid := []string{
		"", ".", "..", "../etc", "a/b", `a\b`, "../../passwd", "a..b",
	}
	for _, id := range invalid {
		if validScopeID(id) {
			t.Errorf("%q should be rejected (path traversal guard)", id)
		}
	}
}

func TestValidContextFile(t *testing.T) {
	valid := []string{"INSTRUCTION.md", "MEMORY.md", "PREFERENCE.md"}
	for _, f := range valid {
		if !validContextFile(f) {
			t.Errorf("%q should be a valid context file", f)
		}
	}
	// Arbitrary names and traversal attempts must be rejected.
	invalid := []string{
		"", "instruction.md", "../../etc/passwd", "INSTRUCTION.md/../x",
		"AGENTS.md", "other.txt",
	}
	for _, f := range invalid {
		if validContextFile(f) {
			t.Errorf("%q should not be a valid context file (path traversal guard)", f)
		}
	}
}

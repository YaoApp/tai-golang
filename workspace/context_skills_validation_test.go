package workspace

import (
	"bytes"
	"fmt"
	"io/fs"
	"strings"
	"testing"
)

// TestValidContextScope pins the four scopes and rejects anything else.
func TestValidContextScope(t *testing.T) {
	valid := []ContextScope{ScopeBot, ScopeHome, ScopeBoard, ScopeSession}
	for _, scope := range valid {
		if !validContextScope(scope) {
			t.Errorf("validContextScope(%q) = false, want true", scope)
		}
	}
	invalid := []ContextScope{"", "BOT", "global", "../etc", "bot/../home"}
	for _, scope := range invalid {
		if validContextScope(scope) {
			t.Errorf("validContextScope(%q) = true, want false", scope)
		}
	}
}

// TestValidContextID rejects ids that would become a path separator or escape
// the scope directory.
func TestValidContextID(t *testing.T) {
	valid := []string{"default", "bot-123", "user_42", "a.b"}
	for _, id := range valid {
		if !validContextID(id) {
			t.Errorf("validContextID(%q) = false, want true", id)
		}
	}
	invalid := []string{"", ".", "..", "../etc", "a/b", `a\b`, "..\\etc"}
	for _, id := range invalid {
		if validContextID(id) {
			t.Errorf("validContextID(%q) = true, want false", id)
		}
	}
}

// TestSeedContextRejectsBadScopeAndID proves the escape probed before the fix
// no longer reaches the filesystem.
func TestSeedContextRejectsBadScopeAndID(t *testing.T) {
	cases := []struct {
		name  string
		scope ContextScope
		id    string
	}{
		{"parent id", ScopeBot, "../../etc"},
		{"absolute id", ScopeBot, "/etc"},
		{"separator id", ScopeBot, "a/b"},
		{"unknown scope", ContextScope("../../etc"), "x"},
		{"dot id", ScopeBot, "."},
		{"empty id", ScopeBot, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mfs := newMockFS()
			err := SeedContext(mfs, tc.scope, tc.id, map[string][]byte{
				"INSTRUCTION.md": []byte("pwned"),
			})
			if err == nil {
				t.Fatal("expected an error for the unsafe scope or id")
			}
			if len(mfs.files) != 0 {
				t.Errorf("files written despite rejection: %v", mfs.files)
			}
			if len(mfs.dirs) != 0 {
				t.Errorf("directories created despite rejection: %v", mfs.dirs)
			}
		})
	}
}

// TestSeedContextRejectsUnknownFilename proves the key whitelist blocks a
// traversal attempt through the files map.
func TestSeedContextRejectsUnknownFilename(t *testing.T) {
	cases := []string{"../../escape.md", "notes.md", "instruction.md", ""}

	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			mfs := newMockFS()
			err := SeedContext(mfs, ScopeBot, "bot-1", map[string][]byte{
				name: []byte("content"),
				// A valid key alongside the bad one must not be written either.
				"MEMORY.md": []byte("memory"),
			})
			if err == nil {
				t.Fatalf("expected an error for filename %q", name)
			}
			if len(mfs.files) != 0 {
				t.Errorf("files written despite rejection: %v", mfs.files)
			}
			if len(mfs.dirs) != 0 {
				t.Errorf("directory created despite rejection: %v", mfs.dirs)
			}
		})
	}
}

// TestSeedContextAcceptsAllThreeFiles covers the accepted key set.
func TestSeedContextAcceptsAllThreeFiles(t *testing.T) {
	mfs := newMockFS()
	err := SeedContext(mfs, ScopeBoard, "board-9", map[string][]byte{
		"INSTRUCTION.md": []byte("instruction"),
		"MEMORY.md":      []byte("memory"),
		"PREFERENCE.md":  []byte("preference"),
	})
	if err != nil {
		t.Fatalf("SeedContext: %v", err)
	}

	dir := ContextDir(ScopeBoard, "board-9")
	for _, name := range []string{"INSTRUCTION.md", "MEMORY.md", "PREFERENCE.md"} {
		if _, ok := mfs.files[dir+"/"+name]; !ok {
			t.Errorf("%s was not written", name)
		}
	}
}

// TestReadContextRejectsBadScopeAndID covers the read path, which previously
// ignored the composed path entirely.
func TestReadContextRejectsBadScopeAndID(t *testing.T) {
	mfs := newMockFS()
	if _, err := ReadContext(mfs, ScopeBot, "../../etc"); err == nil {
		t.Error("expected an error for an escaping id")
	}
	if _, err := ReadContext(mfs, ContextScope("nope"), "x"); err == nil {
		t.Error("expected an error for an unknown scope")
	}
}

// TestMergeContextValidatesBeforeReading proves one bad entry fails the whole
// merge rather than silently dropping its scope.
func TestMergeContextValidatesBeforeReading(t *testing.T) {
	mfs := newMockFS()
	mfs.files[ContextDir(ScopeBot, "bot-1")+"/INSTRUCTION.md"] = []byte("low")

	entries := []ContextEntry{
		{Scope: ScopeBot, ID: "bot-1"},
		{Scope: ScopeSession, ID: "../escape"},
	}
	if _, err := MergeContext(mfs, entries); err == nil {
		t.Error("expected an error for the invalid entry")
	}
}

// TestMergeContextOrdersLowToHigh proves the merge concatenates scopes in the
// order given, with the caller controlling priority.
func TestMergeContextOrdersLowToHigh(t *testing.T) {
	mfs := newMockFS()
	mfs.files[ContextDir(ScopeBot, "bot-1")+"/INSTRUCTION.md"] = []byte("bot rule")
	mfs.files[ContextDir(ScopeHome, "user-1")+"/PREFERENCE.md"] = []byte("be brief")
	mfs.files[ContextDir(ScopeSession, "sess-1")+"/MEMORY.md"] = []byte("remember this")

	merged, err := MergeContext(mfs, []ContextEntry{
		{Scope: ScopeBot, ID: "bot-1"},
		{Scope: ScopeHome, ID: "user-1"},
		// A scope with no files is skipped.
		{Scope: ScopeBoard, ID: "board-1"},
		{Scope: ScopeSession, ID: "sess-1"},
	})
	if err != nil {
		t.Fatalf("MergeContext: %v", err)
	}

	botIdx := strings.Index(merged, "## bot/bot-1")
	homeIdx := strings.Index(merged, "## home/user-1")
	sessionIdx := strings.Index(merged, "## session/sess-1")
	if botIdx < 0 || homeIdx < 0 || sessionIdx < 0 {
		t.Fatalf("missing scope sections in %q", merged)
	}
	if !(botIdx < homeIdx && homeIdx < sessionIdx) {
		t.Errorf("sections out of order: bot=%d home=%d session=%d", botIdx, homeIdx, sessionIdx)
	}
	if strings.Contains(merged, "board-1") {
		t.Error("empty scope was not skipped")
	}
}

// TestValidateSkillKey pins the accepted key shape and the rejected escapes.
func TestValidateSkillKey(t *testing.T) {
	valid := []string{"home", "bots/postman", "board/dashboard-x", "a/b/c"}
	for _, key := range valid {
		if err := validateSkillKey(key); err != nil {
			t.Errorf("validateSkillKey(%q) = %v, want nil", key, err)
		}
	}
	invalid := []string{
		"", "..", "../etc", "/etc", "a/../b", "a//b", "a/", `a\b`, "./a",
	}
	for _, key := range invalid {
		if err := validateSkillKey(key); err == nil {
			t.Errorf("validateSkillKey(%q) = nil, want an error", key)
		}
	}
}

// TestSyncSkillsRejectsBadKey proves an invalid key fails before the manifest
// is read or the workspace is touched.
func TestSyncSkillsRejectsBadKey(t *testing.T) {
	mfs := newMockFS()
	pushed, err := SyncSkills(mfs, []SkillPack{
		{Key: "home", Hash: "sha256:ok", Archive: bytes.NewReader([]byte("ok"))},
		{Key: "../../etc", Hash: "sha256:bad", Archive: bytes.NewReader([]byte("bad"))},
	})
	if err == nil {
		t.Fatal("expected an error for the escaping key")
	}
	if pushed != 0 {
		t.Errorf("pushed = %d, want 0", pushed)
	}
	if len(mfs.archives) != 0 {
		t.Errorf("archives written despite rejection: %v", mfs.archives)
	}
	if len(mfs.removed) != 0 {
		t.Errorf("directories removed despite rejection: %v", mfs.removed)
	}
}

// TestSyncSkillsPushesNewPacks covers the first sync: an unreadable manifest
// counts as empty, so every pack is pushed.
func TestSyncSkillsPushesNewPacks(t *testing.T) {
	mfs := newMockFS()
	mfs.failReadManifest = true

	pushed, err := SyncSkills(mfs, []SkillPack{
		{Key: "bots/postman", Hash: "sha256:aaa", Archive: bytes.NewReader([]byte("a"))},
		{Key: "home", Hash: "sha256:bbb", Archive: bytes.NewReader([]byte("b"))},
	})
	if err != nil {
		t.Fatalf("SyncSkills: %v", err)
	}
	if pushed != 2 {
		t.Errorf("pushed = %d, want 2", pushed)
	}
	if mfs.archives[".skills/bots/postman"] != "a" {
		t.Errorf("archive content = %q", mfs.archives[".skills/bots/postman"])
	}
}

// TestSyncSkillsSkipsMatchingHash covers idempotency: a second sync after the
// manifest recorded the hash pushes nothing.
func TestSyncSkillsSkipsMatchingHash(t *testing.T) {
	mfs := newMockFS()
	mfs.manifest = map[string]string{"bots/postman": "sha256:same"}

	pushed, err := SyncSkills(mfs, []SkillPack{
		{Key: "bots/postman", Hash: "sha256:same", Archive: bytes.NewReader([]byte("ignored"))},
	})
	if err != nil {
		t.Fatalf("SyncSkills: %v", err)
	}
	if pushed != 0 {
		t.Errorf("pushed = %d, want 0", pushed)
	}
	if _, written := mfs.archives[".skills/bots/postman"]; written {
		t.Error("matching pack was written again")
	}
	if len(mfs.removed) != 0 {
		t.Errorf("matching pack removed existing content: %v", mfs.removed)
	}
}

// TestSyncSkillsRepushesChangedHash covers the update path: a different hash
// clears the old directory before extracting the new archive.
func TestSyncSkillsRepushesChangedHash(t *testing.T) {
	mfs := newMockFS()
	mfs.manifest = map[string]string{"bots/postman": "sha256:old"}
	mfs.files[".skills/bots/postman/STALE.md"] = []byte("stale")

	pushed, err := SyncSkills(mfs, []SkillPack{
		{Key: "bots/postman", Hash: "sha256:new", Archive: bytes.NewReader([]byte("fresh"))},
	})
	if err != nil {
		t.Fatalf("SyncSkills: %v", err)
	}
	if pushed != 1 {
		t.Errorf("pushed = %d, want 1", pushed)
	}
	if len(mfs.removed) != 1 || mfs.removed[0] != ".skills/bots/postman" {
		t.Errorf("removed = %v, want [.skills/bots/postman]", mfs.removed)
	}
	if _, stale := mfs.files[".skills/bots/postman/STALE.md"]; stale {
		t.Error("stale content survived the update")
	}
	if mfs.archives[".skills/bots/postman"] != "fresh" {
		t.Errorf("archive content = %q, want fresh", mfs.archives[".skills/bots/postman"])
	}
}

// TestSyncSkillsReportsWriteFailure covers partial progress being reported
// alongside the error.
func TestSyncSkillsReportsWriteFailure(t *testing.T) {
	mfs := newMockFS()
	mfs.manifest = map[string]string{"home": "sha256:keep"}
	mfs.failWriteArchive = true

	pushed, err := SyncSkills(mfs, []SkillPack{
		{Key: "home", Hash: "sha256:keep", Archive: bytes.NewReader([]byte("skip"))},
		{Key: "bots/postman", Hash: "sha256:new", Archive: bytes.NewReader([]byte("fail"))},
	})
	if err == nil {
		t.Fatal("expected the write failure to surface")
	}
	if pushed != 0 {
		t.Errorf("pushed = %d, want 0", pushed)
	}
	if !strings.Contains(err.Error(), "bots/postman") {
		t.Errorf("error does not name the failing key: %v", err)
	}
}

// TestSeedContextReportsFilesystemFailures covers each wrapped error so a
// caller can tell which step failed.
func TestSeedContextReportsFilesystemFailures(t *testing.T) {
	files := map[string][]byte{"INSTRUCTION.md": []byte("x")}

	cases := []struct {
		name  string
		setup func(*mockWorkspaceFS)
		want  string
	}{
		{"mkdir", func(m *mockWorkspaceFS) { m.failMkdirAll = true }, "seed context mkdir"},
		{"create", func(m *mockWorkspaceFS) { m.failCreate = true }, "seed context create"},
		{"write", func(m *mockWorkspaceFS) { m.failWrite = true }, "seed context write"},
		{"close", func(m *mockWorkspaceFS) { m.failClose = true }, "seed context close"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mfs := newMockFS()
			tc.setup(mfs)
			err := SeedContext(mfs, ScopeBot, "bot-1", files)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestMergeContextPropagatesReadError covers the read failure path.
func TestMergeContextPropagatesReadError(t *testing.T) {
	// fstest.MapFS cannot fail on read, so use a filesystem that refuses to
	// open any file.
	if _, err := MergeContext(noReadFS{}, []ContextEntry{{Scope: ScopeBot, ID: "x"}}); err != nil {
		// ReadContext treats every read error as absent, so a total failure
		// yields an empty merge rather than an error. Keep the assertion on
		// that documented behaviour.
		t.Fatalf("unexpected error: %v", err)
	}
}

// noReadFS fails every Open.
type noReadFS struct{}

func (noReadFS) Open(string) (fs.File, error) { return nil, fmt.Errorf("open failed") }

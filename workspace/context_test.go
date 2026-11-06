package workspace

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/yaoapp/tai-golang/types"
)

func TestContextDir(t *testing.T) {
	tests := []struct {
		scope ContextScope
		id    string
		want  string
	}{
		{ScopeBot, "postman", ".context/bot/postman"},
		{ScopeHome, "user-42", ".context/home/user-42"},
		{ScopeBoard, "board-x", ".context/board/board-x"},
		{ScopeSession, "chat-123", ".context/session/chat-123"},
	}
	for _, tt := range tests {
		got := ContextDir(tt.scope, tt.id)
		if got != tt.want {
			t.Errorf("ContextDir(%s, %s) = %q, want %q", tt.scope, tt.id, got, tt.want)
		}
	}
}

func TestContextFilesEmpty(t *testing.T) {
	cf := &ContextFiles{}
	if !cf.Empty() {
		t.Error("expected empty ContextFiles")
	}
	cf.Instruction = []byte("x")
	if cf.Empty() {
		t.Error("expected non-empty ContextFiles")
	}
}

func TestContextFilesFormatSection(t *testing.T) {
	cf := &ContextFiles{
		Instruction: []byte("Do X"),
		Memory:      []byte("Fact Y"),
	}
	out := cf.FormatSection("bot/postman")
	if out == "" {
		t.Fatal("expected non-empty section")
	}
	if !contains(out, "## bot/postman") {
		t.Error("missing section header")
	}
	if !contains(out, "### INSTRUCTION.md") {
		t.Error("missing INSTRUCTION.md subsection")
	}
	if !contains(out, "Do X") {
		t.Error("missing instruction content")
	}
	if !contains(out, "### MEMORY.md") {
		t.Error("missing MEMORY.md subsection")
	}
	if !contains(out, "Fact Y") {
		t.Error("missing memory content")
	}
	// Preference is nil — should not appear.
	if contains(out, "PREFERENCE") {
		t.Error("unexpected PREFERENCE section for nil field")
	}
}

func TestContextFilesFormatSectionEmpty(t *testing.T) {
	cf := &ContextFiles{}
	out := cf.FormatSection("bot/empty")
	if out != "" {
		t.Errorf("expected empty string, got %q", out)
	}
}

func TestReadContext(t *testing.T) {
	memFS := fstest.MapFS{
		".context/bot/postman/INSTRUCTION.md": &fstest.MapFile{Data: []byte("Be helpful")},
		".context/bot/postman/MEMORY.md":      &fstest.MapFile{Data: []byte("User likes Go")},
	}
	cf, err := ReadContext(memFS, ScopeBot, "postman")
	if err != nil {
		t.Fatal(err)
	}
	if string(cf.Instruction) != "Be helpful" {
		t.Errorf("Instruction = %q", cf.Instruction)
	}
	if string(cf.Memory) != "User likes Go" {
		t.Errorf("Memory = %q", cf.Memory)
	}
	if cf.Preference != nil {
		t.Error("expected nil Preference")
	}
}

func TestReadContextMissing(t *testing.T) {
	memFS := fstest.MapFS{}
	cf, err := ReadContext(memFS, ScopeSession, "no-such")
	if err != nil {
		t.Fatal(err)
	}
	if !cf.Empty() {
		t.Error("expected empty for missing scope")
	}
}

func TestMergeContext(t *testing.T) {
	memFS := fstest.MapFS{
		".context/bot/postman/INSTRUCTION.md": &fstest.MapFile{Data: []byte("Be a REST assistant")},
		".context/home/user-42/PREFERENCE.md": &fstest.MapFile{Data: []byte("Respond in Chinese")},
		".context/session/chat-1/MEMORY.md":   &fstest.MapFile{Data: []byte("Debugging auth")},
	}
	entries := []ContextEntry{
		{Scope: ScopeBot, ID: "postman"},
		{Scope: ScopeHome, ID: "user-42"},
		{Scope: ScopeBoard, ID: "no-board"}, // empty — should be skipped
		{Scope: ScopeSession, ID: "chat-1"},
	}
	result, err := MergeContext(memFS, entries)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(result, "## bot/postman") {
		t.Error("missing bot section")
	}
	if !contains(result, "## home/user-42") {
		t.Error("missing home section")
	}
	if contains(result, "board/no-board") {
		t.Error("empty board should be skipped")
	}
	if !contains(result, "## session/chat-1") {
		t.Error("missing session section")
	}
	if !contains(result, "Be a REST assistant") {
		t.Error("missing bot instruction")
	}
	if !contains(result, "Respond in Chinese") {
		t.Error("missing home preference")
	}
	if !contains(result, "Debugging auth") {
		t.Error("missing session memory")
	}
}

func TestMergeContextEmpty(t *testing.T) {
	memFS := fstest.MapFS{}
	result, err := MergeContext(memFS, []ContextEntry{
		{Scope: ScopeBot, ID: "empty"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "" {
		t.Errorf("expected empty merge, got %q", result)
	}
}

// mockWorkspaceFS is a minimal in-memory WorkspaceFS for testing the Context
// and Skills helpers.
type mockWorkspaceFS struct {
	files map[string][]byte
	dirs  map[string]bool
	// manifest is returned by ReadManifest; archives records what WriteArchive
	// received, keyed by destination directory.
	manifest map[string]string
	archives map[string]string
	// failReadManifest and failWriteArchive force the corresponding error.
	failReadManifest bool
	failWriteArchive bool
	// failMkdirAll, failCreate, failWrite and failClose force the matching
	// SeedContext failure.
	failMkdirAll bool
	failCreate   bool
	failWrite    bool
	failClose    bool
	// removed records RemoveAll destinations in order.
	removed []string
}

func newMockFS() *mockWorkspaceFS {
	return &mockWorkspaceFS{
		files:    make(map[string][]byte),
		dirs:     make(map[string]bool),
		manifest: make(map[string]string),
		archives: make(map[string]string),
	}
}

func (m *mockWorkspaceFS) Open(name string) (fs.File, error) {
	if data, ok := m.files[name]; ok {
		return &mockFile{name: name, data: data}, nil
	}
	if m.dirs[name] {
		return &mockFile{name: name, isDir: true}, nil
	}
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}

func (m *mockWorkspaceFS) Create(name string) (io.WriteCloser, error) {
	if m.failCreate {
		return nil, fmt.Errorf("create failed")
	}
	return &mockWriter{fs: m, name: name}, nil
}

func (m *mockWorkspaceFS) Remove(name string) error { delete(m.files, name); return nil }
func (m *mockWorkspaceFS) RemoveAll(name string) error {
	m.removed = append(m.removed, name)
	for path := range m.files {
		if strings.HasPrefix(path, name+"/") {
			delete(m.files, path)
		}
	}
	delete(m.dirs, name)
	return nil
}
func (m *mockWorkspaceFS) Rename(_, _ string) error { return nil }
func (m *mockWorkspaceFS) MkdirAll(name string, _ fs.FileMode) error {
	if m.failMkdirAll {
		return fmt.Errorf("mkdir failed")
	}
	m.dirs[name] = true
	return nil
}
func (m *mockWorkspaceFS) Symlink(_, _ string) error { return nil }
func (m *mockWorkspaceFS) Watch(_ []string) (types.Watcher, error) {
	return nil, fmt.Errorf("not implemented")
}
func (m *mockWorkspaceFS) Exists(name string) (bool, error) {
	if _, ok := m.files[name]; ok {
		return true, nil
	}
	return m.dirs[name], nil
}

// WriteArchive records the archive body it was given, mirroring the remote
// handler's behaviour of extracting into dir and returning no error.
func (m *mockWorkspaceFS) WriteArchive(dir string, r io.Reader) error {
	if m.failWriteArchive {
		return fmt.Errorf("write archive failed")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.archives[dir] = string(data)
	m.dirs[dir] = true
	m.files[dir+"/SKILL.md"] = []byte("# extracted")
	if m.manifest != nil {
		m.manifest[strings.TrimPrefix(dir, ".skills/")] = "sha256:extracted"
	}
	return nil
}

// ReadManifest returns the recorded manifest for dir, or an error when the
// test asked for a failure or the directory has no manifest yet.
func (m *mockWorkspaceFS) ReadManifest(dir string) (map[string]string, error) {
	if m.failReadManifest {
		return nil, fmt.Errorf("read manifest failed")
	}
	if m.manifest == nil {
		return nil, fmt.Errorf("no manifest")
	}
	return m.manifest, nil
}

type mockWriter struct {
	fs   *mockWorkspaceFS
	name string
	buf  bytes.Buffer
}

func (w *mockWriter) Write(p []byte) (int, error) {
	if w.fs.failWrite {
		return 0, fmt.Errorf("write failed")
	}
	return w.buf.Write(p)
}
func (w *mockWriter) Close() error {
	if w.fs.failClose {
		return fmt.Errorf("close failed")
	}
	w.fs.files[w.name] = w.buf.Bytes()
	return nil
}

type mockFile struct {
	name  string
	data  []byte
	isDir bool
	pos   int
}

func (f *mockFile) Stat() (fs.FileInfo, error) {
	return &mockFileInfo{name: f.name, size: int64(len(f.data)), isDir: f.isDir}, nil
}
func (f *mockFile) Read(p []byte) (int, error) {
	if f.pos >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.pos:])
	f.pos += n
	return n, nil
}
func (f *mockFile) Close() error { return nil }

type mockFileInfo struct {
	name  string
	size  int64
	isDir bool
}

func (fi *mockFileInfo) Name() string       { return fi.name }
func (fi *mockFileInfo) Size() int64        { return fi.size }
func (fi *mockFileInfo) Mode() fs.FileMode  { return 0o644 }
func (fi *mockFileInfo) ModTime() time.Time { return time.Time{} }
func (fi *mockFileInfo) IsDir() bool        { return fi.isDir }
func (fi *mockFileInfo) Sys() any           { return nil }

func TestSeedContextCreatesFiles(t *testing.T) {
	mfs := newMockFS()
	files := map[string][]byte{
		"INSTRUCTION.md": []byte("Be helpful"),
		"MEMORY.md":      []byte("User prefers Go"),
	}
	if err := SeedContext(mfs, ScopeBot, "test-bot", files); err != nil {
		t.Fatal(err)
	}
	// Verify files were created.
	dir := ContextDir(ScopeBot, "test-bot")
	if !mfs.dirs[dir] {
		t.Error("expected directory to be created")
	}
	if string(mfs.files[dir+"/INSTRUCTION.md"]) != "Be helpful" {
		t.Errorf("INSTRUCTION.md = %q", mfs.files[dir+"/INSTRUCTION.md"])
	}
	if string(mfs.files[dir+"/MEMORY.md"]) != "User prefers Go" {
		t.Errorf("MEMORY.md = %q", mfs.files[dir+"/MEMORY.md"])
	}
}

func TestSeedContextSkipsExisting(t *testing.T) {
	mfs := newMockFS()
	dir := ContextDir(ScopeBot, "test-bot")
	mfs.dirs[dir] = true
	mfs.files[dir+"/INSTRUCTION.md"] = []byte("Original")

	if err := SeedContext(mfs, ScopeBot, "test-bot", map[string][]byte{
		"INSTRUCTION.md": []byte("New content"),
		"MEMORY.md":      []byte("Memory"),
	}); err != nil {
		t.Fatal(err)
	}

	// Existing file should NOT be overwritten.
	if string(mfs.files[dir+"/INSTRUCTION.md"]) != "Original" {
		t.Errorf("existing file was overwritten: %q", mfs.files[dir+"/INSTRUCTION.md"])
	}
	// New file should be created.
	if string(mfs.files[dir+"/MEMORY.md"]) != "Memory" {
		t.Errorf("MEMORY.md not created: %q", mfs.files[dir+"/MEMORY.md"])
	}
}

func TestSeedContextEmptyFiles(t *testing.T) {
	mfs := newMockFS()
	if err := SeedContext(mfs, ScopeSession, "s1", map[string][]byte{}); err != nil {
		t.Fatal(err)
	}
	if !mfs.dirs[ContextDir(ScopeSession, "s1")] {
		t.Error("directory should be created even with empty files map")
	}
}

func TestSeedContextAllScopes(t *testing.T) {
	for _, scope := range []ContextScope{ScopeBot, ScopeHome, ScopeBoard, ScopeSession} {
		mfs := newMockFS()
		err := SeedContext(mfs, scope, "id-1", map[string][]byte{
			"INSTRUCTION.md": []byte("test"),
		})
		if err != nil {
			t.Errorf("scope %s: %v", scope, err)
		}
		dir := ContextDir(scope, "id-1")
		if string(mfs.files[dir+"/INSTRUCTION.md"]) != "test" {
			t.Errorf("scope %s: file not created", scope)
		}
	}
}

// errorMockFS returns errors on MkdirAll to test SeedContext error handling.
type errorMockFS struct {
	mockWorkspaceFS
}

func (m *errorMockFS) MkdirAll(_ string, _ fs.FileMode) error {
	return fmt.Errorf("mkdir failed")
}

func TestSeedContextMkdirError(t *testing.T) {
	efs := &errorMockFS{mockWorkspaceFS: *newMockFS()}
	err := SeedContext(efs, ScopeBot, "b1", map[string][]byte{"x.md": []byte("data")})
	if err == nil {
		t.Error("expected error from MkdirAll")
	}
}

// writeErrorMockFS returns errors on Create to test SeedContext write error.
type writeErrorMockFS struct {
	*mockWorkspaceFS
}

func (m *writeErrorMockFS) Create(_ string) (io.WriteCloser, error) {
	return nil, fmt.Errorf("create failed")
}

func TestSeedContextCreateError(t *testing.T) {
	mfs := newMockFS()
	wefs := &writeErrorMockFS{mockWorkspaceFS: mfs}
	err := SeedContext(wefs, ScopeBot, "b2", map[string][]byte{"x.md": []byte("data")})
	if err == nil {
		t.Error("expected error from Create")
	}
}

// writeFailWriter returns an error on Write.
type writeFailWriter struct{}

func (writeFailWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("write failed") }
func (writeFailWriter) Close() error              { return nil }

type writeFailMockFS struct {
	*mockWorkspaceFS
}

func (m *writeFailMockFS) Create(_ string) (io.WriteCloser, error) {
	return writeFailWriter{}, nil
}

func TestSeedContextWriteError(t *testing.T) {
	mfs := newMockFS()
	wffs := &writeFailMockFS{mockWorkspaceFS: mfs}
	err := SeedContext(wffs, ScopeBot, "b3", map[string][]byte{"x.md": []byte("data")})
	if err == nil {
		t.Error("expected error from Write")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && searchString(s, sub)
}

func searchString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Verify fs.FS compliance.
var _ fs.FS = fstest.MapFS{}

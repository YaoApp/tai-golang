package plugin

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yaoapp/tai-golang/pb"
)

func entryAt(t *testing.T, data []byte) *pb.PluginEntry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plugin.wasm")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return &pb.PluginEntry{
		Name: "echo", Version: "1.0.0", Runtime: "wasm", ArtifactPath: path,
		Capabilities: &pb.Capabilities{Args: []string{"a"}, Env: []string{"E"}, Fs: []*pb.FSGrant{{Mount: "tmp", Mode: "rw"}},
			Fetch: &pb.FetchCapability{Allow: []string{"*"}, Methods: []string{"GET"}, MaxCalls: 1}},
	}
}

func entry() *pb.PluginEntry { return &pb.PluginEntry{Name: "echo", Version: "1.0.0", Runtime: "wasm"} }

func TestRegistry(t *testing.T) {
	changed := 0
	r := New(func() { changed++ })
	e := entryAt(t, []byte("wasm"))
	if err := r.RegisterPlugin(e); err != nil {
		t.Fatal(err)
	}
	if e.Digest == "" || e.Size == 0 || changed != 1 {
		t.Fatalf("entry = %+v changed=%d", e, changed)
	}
	meta := r.Metadata()
	if len(meta) != 1 || meta[0].ArtifactPath != "" || meta[0].Capabilities.Fetch == nil {
		t.Fatalf("meta = %+v", meta)
	}
	rc, size, err := r.OpenArtifact(e.Digest)
	if err != nil || size != 4 {
		t.Fatalf("artifact %d %v", size, err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "wasm" {
		t.Fatalf("artifact %q", data)
	}
	if _, _, err := r.OpenArtifact("sha256:no"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected not found")
	}
	if err := r.RegisterPlugin(e); !errors.Is(err, ErrInvalidPlugin) {
		t.Fatalf("duplicate = %v", err)
	}
	if err := r.UnregisterPlugin("echo", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := r.UnregisterPlugin("echo", "1.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected not found")
	}
}

func TestRegistryValidation(t *testing.T) {
	r := New(nil)
	cases := []*pb.PluginEntry{
		nil,
		{Name: "Bad", Version: "1.0.0", Runtime: "wasm", ArtifactPath: "/tmp/x"},
		{Name: "ok", Version: "latest", Runtime: "wasm", ArtifactPath: "/tmp/x"},
		{Name: "ok", Version: "1.0.0", Runtime: "native", ArtifactPath: "/tmp/x"},
		{Name: "ok", Version: "1.0.0", Runtime: "wasm"},
	}
	for i, e := range cases {
		if err := r.RegisterPlugin(e); err == nil {
			t.Errorf("case %d expected error", i)
		}
	}
	if err := r.SetRemotePlugins([]*pb.PluginEntry{entryAt(t, []byte("wasm"))}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetRemotePlugins([]*pb.PluginEntry{{Name: "Bad"}}); err == nil {
		t.Fatal("expected set error")
	}
	if DigestHex("sha256:abc") != "abc" {
		t.Fatal("digest hex")
	}
}

func TestSetRemotePluginsAtomic(t *testing.T) {
	r := New(nil)
	first := entryAt(t, []byte("wasm"))
	if err := r.SetRemotePlugins([]*pb.PluginEntry{first}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetRemotePlugins([]*pb.PluginEntry{{Name: "bad"}}); err == nil {
		t.Fatal("expected error")
	}
	if got := r.Metadata(); len(got) != 1 || got[0].Name != "echo" {
		t.Fatalf("registry not preserved: %+v", got)
	}
}

func TestArtifactHexDigest(t *testing.T) {
	r := New(nil)
	e := entryAt(t, []byte("x"))
	e.Name = "p"
	if err := r.RegisterPlugin(e); err != nil {
		t.Fatal(err)
	}
	hex := strings.TrimPrefix(e.Digest, "sha256:")
	rc, _, err := r.OpenArtifact(hex)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if string(data) != "x" {
		t.Fatalf("artifact %q", data)
	}
}

func TestPluginArtifactLimit(t *testing.T) {
	r := New(nil)
	path := filepath.Join(t.TempDir(), "big.wasm")
	if err := os.WriteFile(path, make([]byte, MaxPluginArtifact+1), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := &pb.PluginEntry{Name: "big", Version: "1.0.0", Runtime: "wasm", ArtifactPath: path}
	if err := r.RegisterPlugin(entry); err == nil {
		t.Fatal("expected limit error")
	}
}

func TestArtifactPathErrors(t *testing.T) {
	r := New(nil)
	if err := r.RegisterPlugin(&pb.PluginEntry{Name: "p", Version: "1.0.0", Runtime: "wasm", ArtifactPath: filepath.Join(t.TempDir(), "missing.wasm")}); err == nil {
		t.Fatal("expected missing artifact error")
	}
	empty := filepath.Join(t.TempDir(), "empty.wasm")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterPlugin(&pb.PluginEntry{Name: "p", Version: "1.0.0", Runtime: "wasm", ArtifactPath: empty}); err == nil {
		t.Fatal("expected empty artifact error")
	}
	if err := r.SetRemotePlugins([]*pb.PluginEntry{{Name: "p", Version: "1.0.0", Runtime: "wasm", ArtifactPath: filepath.Join(t.TempDir(), "missing.wasm")}}); err == nil {
		t.Fatal("expected set path error")
	}
}

func TestOpenArtifactErrorsAndMetadataOrder(t *testing.T) {
	r := New(nil)
	a := entryAt(t, []byte("a"))
	a.Name, a.Version = "z.tool", "1.0.0"
	b := entryAt(t, []byte("b"))
	b.Name, b.Version = "a.tool", "1.0.0"
	if err := r.RegisterPlugin(a); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterPlugin(b); err != nil {
		t.Fatal(err)
	}
	meta := r.Metadata()
	if len(meta) != 2 || meta[0].Name != "a.tool" || meta[1].Name != "z.tool" {
		t.Fatalf("metadata order=%+v", meta)
	}
	// File changed after registration -> size mismatch.
	if err := os.WriteFile(a.ArtifactPath, []byte("changed!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.OpenArtifact(a.Digest); err == nil {
		t.Fatal("expected changed artifact error")
	}
	// Missing file after registration.
	if err := os.Remove(b.ArtifactPath); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.OpenArtifact(b.Digest); err == nil {
		t.Fatal("expected open error")
	}
}

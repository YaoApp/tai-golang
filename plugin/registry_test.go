package plugin

import (
	"errors"
	"strings"
	"testing"

	"github.com/yaoapp/tai-golang/pb"
)

func entry() *pb.PluginEntry {
	return &pb.PluginEntry{
		Name: "echo", Version: "1.0.0", Runtime: "wasm", Artifact: []byte("wasm"),
		Capabilities: &pb.Capabilities{Args: []string{"a"}, Env: []string{"E"}, Fs: []*pb.FSGrant{{Mount: "tmp", Mode: "rw"}},
			Fetch: &pb.FetchCapability{Allow: []string{"*"}, Methods: []string{"GET"}, MaxCalls: 1}},
	}
}

func TestRegistry(t *testing.T) {
	changed := 0
	r := New(func() { changed++ })
	e := entry()
	if err := r.RegisterPlugin(e); err != nil {
		t.Fatal(err)
	}
	if e.Digest == "" || e.Size == 0 || changed != 1 {
		t.Fatalf("entry = %+v changed=%d", e, changed)
	}
	meta := r.Metadata()
	if len(meta) != 1 || len(meta[0].Artifact) != 0 || meta[0].Capabilities.Fetch == nil {
		t.Fatalf("meta = %+v", meta)
	}
	data, err := r.Artifact(e.Digest)
	if err != nil || string(data) != "wasm" {
		t.Fatalf("artifact %q %v", data, err)
	}
	if _, err := r.Artifact("sha256:no"); !errors.Is(err, ErrNotFound) {
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
		{Name: "Bad", Version: "1.0.0", Runtime: "wasm", Artifact: []byte("x")},
		{Name: "ok", Version: "latest", Runtime: "wasm", Artifact: []byte("x")},
		{Name: "ok", Version: "1.0.0", Runtime: "native", Artifact: []byte("x")},
		{Name: "ok", Version: "1.0.0", Runtime: "wasm"},
	}
	for i, e := range cases {
		if err := r.RegisterPlugin(e); err == nil {
			t.Errorf("case %d expected error", i)
		}
	}
	if err := r.SetRemotePlugins([]*pb.PluginEntry{entry()}); err != nil {
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
	first := entry()
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
	e := &pb.PluginEntry{Name: "p", Version: "1.0.0", Runtime: "wasm", Artifact: []byte("x")}
	if err := r.RegisterPlugin(e); err != nil {
		t.Fatal(err)
	}
	hex := strings.TrimPrefix(e.Digest, "sha256:")
	if data, err := r.Artifact(hex); err != nil || string(data) != "x" {
		t.Fatalf("artifact %q %v", data, err)
	}
}

func TestPluginArtifactLimit(t *testing.T) {
	r := New(nil)
	entry := &pb.PluginEntry{Name: "big", Version: "1.0.0", Runtime: "wasm", Artifact: make([]byte, MaxPluginArtifact+1)}
	if err := r.RegisterPlugin(entry); err == nil {
		t.Fatal("expected limit error")
	}
}

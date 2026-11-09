package plugins

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yaoapp/tai-golang/pb"
)

type fakeReg struct{ entries []*pb.PluginEntry }

func (f *fakeReg) RegisterPlugin(e *pb.PluginEntry) error {
	f.entries = append(f.entries, e)
	return nil
}

func TestRegisterArtifacts(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeReg{}
	if err := Register(fake, dir); err == nil {
		t.Fatal("expected missing artifact error")
	}
	for _, n := range []string{"normalize", "tag", "bad_fetch"} {
		if err := os.WriteFile(filepath.Join(dir, n+".wasm"), []byte(n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := Register(fake, dir); err != nil {
		t.Fatal(err)
	}
	if len(fake.entries) != 3 || fake.entries[0].Name != "demo.normalize" || len(fake.entries[0].ArtifactPath) == 0 {
		t.Fatalf("entries=%+v", fake.entries)
	}
}

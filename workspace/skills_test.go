package workspace

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestSkillHash(t *testing.T) {
	data := []byte("hello skills")
	hash, err := SkillHash(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "sha256:") {
		t.Errorf("hash should start with sha256: prefix, got %q", hash)
	}
	if len(hash) != len("sha256:")+64 {
		t.Errorf("unexpected hash length: %d", len(hash))
	}

	// Same input should produce same hash.
	hash2, _ := SkillHash(bytes.NewReader(data))
	if hash != hash2 {
		t.Error("same input should produce same hash")
	}

	// Different input should produce different hash.
	hash3, _ := SkillHash(bytes.NewReader([]byte("other")))
	if hash == hash3 {
		t.Error("different input should produce different hash")
	}
}

func TestSkillHashEmpty(t *testing.T) {
	hash, err := SkillHash(bytes.NewReader(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "sha256:") {
		t.Errorf("expected sha256: prefix, got %q", hash)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, fmt.Errorf("read error")
}

func TestSkillHashReadError(t *testing.T) {
	_, err := SkillHash(errorReader{})
	if err == nil {
		t.Error("expected error for failing reader")
	}
}

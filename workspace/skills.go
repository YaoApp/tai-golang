package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/yaoapp/tai-golang/types"
)

// SkillPack describes a skill package to sync to the workspace.
type SkillPack struct {
	Key     string    // manifest key, e.g. "bots/postman", "home", "board/dashboard-x"
	Hash    string    // pre-computed hash via SkillHash ("sha256:hex...")
	Archive io.Reader // tar.gz or zip archive; only read when hash differs
}

// SkillHash computes the SHA-256 hash of an archive for use in SkillPack.Hash.
// The returned string has the form "sha256:<hex>".
// Callers should compute the hash before constructing SkillPack to avoid
// consuming the Reader twice.
func SkillHash(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", fmt.Errorf("skill hash: %w", err)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// validateSkillKey reports whether key names a single skill directory under
// .skills/. The key is joined into a path and later removed recursively, so a
// separator or parent reference would escape the skills directory.
//
// @param key manifest key, e.g. "bots/postman"
// @returns an error naming the rejected key, or nil
func validateSkillKey(key string) error {
	if key == "" {
		return fmt.Errorf("skill key is empty")
	}
	if strings.HasPrefix(key, "/") || strings.Contains(key, `\`) {
		return fmt.Errorf("skill key %q must be a relative slash path", key)
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("skill key %q must not contain empty, . or .. segments", key)
		}
	}
	return nil
}

// SyncSkills synchronizes skill packs to the workspace branch under ".skills/".
// It reads the current manifest, compares hashes, and only pushes packs that
// differ. Returns the number of packs actually pushed.
//
// Every key is validated before any manifest read or write, so one bad key
// fails the whole sync without touching the workspace.
func SyncSkills(fsys types.WorkspaceFS, packs []SkillPack) (int, error) {
	for _, pack := range packs {
		if err := validateSkillKey(pack.Key); err != nil {
			return 0, err
		}
	}

	manifest, err := fsys.ReadManifest(".skills")
	if err != nil {
		// No manifest yet — treat as empty.
		manifest = make(map[string]string)
	}

	pushed := 0
	for _, pack := range packs {
		if manifest[pack.Key] == pack.Hash {
			continue
		}
		dir := ".skills/" + pack.Key
		// Remove old content before writing new archive.
		_ = fsys.RemoveAll(dir)
		if err := fsys.WriteArchive(dir, pack.Archive); err != nil {
			return pushed, fmt.Errorf("sync skill %s: %w", pack.Key, err)
		}
		pushed++
	}
	return pushed, nil
}

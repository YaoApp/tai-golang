// Package plugin maintains the SDK-side Plugin registry and artifact store.
package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/yaoapp/tai-golang/pb"
)

// MaxPluginArtifact is the per-plugin artifact limit (design §4).
const MaxPluginArtifact = 16 << 20

var (
	ErrInvalidPlugin = errors.New("plugin_invalid")
	ErrNotFound      = errors.New("plugin_not_found")
	nameRE           = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,63}$`)
	versionRE        = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
)

// Registry is a concurrency-safe Plugin registry.
type Registry struct {
	mu      sync.RWMutex
	plugins map[string]*stored
	changed func()
}

type stored struct {
	meta *pb.PluginEntry
	path string
}

// New creates a registry. changed is called when the set changes.
func New(changed func()) *Registry {
	return &Registry{plugins: map[string]*stored{}, changed: changed}
}

// RegisterPlugin validates a Plugin and computes digest/size from its source
// artifact_path. The file is opened here only to hash it; bytes are not kept in
// memory. The file is opened again when the artifact is transferred.
func (r *Registry) RegisterPlugin(entry *pb.PluginEntry) error {
	if entry == nil || !nameRE.MatchString(entry.Name) || !versionRE.MatchString(entry.Version) {
		return fmt.Errorf("%w: invalid name/version", ErrInvalidPlugin)
	}
	if entry.Runtime != "wasm" {
		return fmt.Errorf("%w: runtime %q", ErrInvalidPlugin, entry.Runtime)
	}
	if entry.ArtifactPath == "" {
		return fmt.Errorf("%w: artifact_path is required", ErrInvalidPlugin)
	}
	digest, size, err := hashArtifactFile(entry.ArtifactPath)
	if err != nil {
		return err
	}
	entry.Digest, entry.Size = digest, size
	key := entry.Name + "@" + entry.Version
	r.mu.Lock()
	if _, ok := r.plugins[key]; ok {
		r.mu.Unlock()
		return fmt.Errorf("%w: duplicate %s", ErrInvalidPlugin, key)
	}
	meta := cloneMeta(entry)
	r.plugins[key] = &stored{meta: meta, path: entry.ArtifactPath}
	r.mu.Unlock()
	r.notify()
	return nil
}

func hashArtifactFile(path string) (string, uint64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("%w: open artifact: %v", ErrInvalidPlugin, err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, MaxPluginArtifact+1))
	if err != nil {
		return "", 0, fmt.Errorf("%w: read artifact: %v", ErrInvalidPlugin, err)
	}
	if n == 0 {
		return "", 0, fmt.Errorf("%w: artifact is empty", ErrInvalidPlugin)
	}
	if n > MaxPluginArtifact {
		return "", 0, fmt.Errorf("%w: artifact exceeds %d bytes", ErrInvalidPlugin, MaxPluginArtifact)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), uint64(n), nil
}

// UnregisterPlugin removes a Plugin.
func (r *Registry) UnregisterPlugin(name, version string) error {
	r.mu.Lock()
	key := name + "@" + version
	if _, ok := r.plugins[key]; !ok {
		r.mu.Unlock()
		return ErrNotFound
	}
	delete(r.plugins, key)
	r.mu.Unlock()
	r.notify()
	return nil
}

// SetRemotePlugins replaces the registry atomically.
func (r *Registry) SetRemotePlugins(entries []*pb.PluginEntry) error {
	next := make(map[string]*stored, len(entries))
	for _, e := range entries {
		if e == nil || !nameRE.MatchString(e.Name) || !versionRE.MatchString(e.Version) || e.Runtime != "wasm" || e.ArtifactPath == "" {
			return fmt.Errorf("%w: invalid entry", ErrInvalidPlugin)
		}
		digest, size, err := hashArtifactFile(e.ArtifactPath)
		if err != nil {
			return err
		}
		e.Digest, e.Size = digest, size
		key := e.Name + "@" + e.Version
		if _, ok := next[key]; ok {
			return fmt.Errorf("%w: duplicate %s", ErrInvalidPlugin, key)
		}
		next[key] = &stored{meta: cloneMeta(e), path: e.ArtifactPath}
	}
	r.mu.Lock()
	r.plugins = next
	r.mu.Unlock()
	r.notify()
	return nil
}

// Metadata returns sync payloads with empty artifact bytes.
func (r *Registry) Metadata() []*pb.PluginEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*pb.PluginEntry, 0, len(r.plugins))
	for _, s := range r.plugins {
		out = append(out, cloneMeta(s.meta))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].Version < out[j].Version
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// OpenArtifact opens the plugin artifact for streaming. Digest is accepted
// with or without the sha256: prefix.
func (r *Registry) OpenArtifact(digest string) (io.ReadCloser, uint64, error) {
	if digest != "" && !strings.HasPrefix(digest, "sha256:") {
		digest = "sha256:" + digest
	}
	r.mu.RLock()
	var path string
	var size uint64
	for _, s := range r.plugins {
		if s.meta.Digest == digest {
			path, size = s.path, s.meta.Size
			break
		}
	}
	r.mu.RUnlock()
	if path == "" {
		return nil, 0, ErrNotFound
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	if info, err := f.Stat(); err != nil || uint64(info.Size()) != size {
		f.Close()
		return nil, 0, fmt.Errorf("%w: artifact changed", ErrInvalidPlugin)
	}
	return f, size, nil
}
func (r *Registry) notify() {
	if r.changed != nil {
		r.changed()
	}
}

func cloneMeta(e *pb.PluginEntry) *pb.PluginEntry {
	cp := &pb.PluginEntry{
		Name: e.Name, Version: e.Version, Runtime: e.Runtime,
		Digest: e.Digest, Size: e.Size, Description: e.Description,
	}
	cp.ArtifactPath = ""
	if e.Capabilities != nil {
		cp.Capabilities = &pb.Capabilities{
			Args: append([]string(nil), e.Capabilities.Args...),
			Env:  append([]string(nil), e.Capabilities.Env...),
		}
		for _, f := range e.Capabilities.Fs {
			cp.Capabilities.Fs = append(cp.Capabilities.Fs, &pb.FSGrant{Mount: f.Mount, Mode: f.Mode})
		}
		if e.Capabilities.Fetch != nil {
			cp.Capabilities.Fetch = &pb.FetchCapability{
				Allow:           append([]string(nil), e.Capabilities.Fetch.Allow...),
				Methods:         append([]string(nil), e.Capabilities.Fetch.Methods...),
				FollowRedirects: e.Capabilities.Fetch.FollowRedirects,
				MaxBodyBytes:    e.Capabilities.Fetch.MaxBodyBytes,
				TimeoutMs:       e.Capabilities.Fetch.TimeoutMs,
				MaxCalls:        e.Capabilities.Fetch.MaxCalls,
			}
		}
	}
	return cp
}

// DigestHex strips the sha256 prefix for content-addressed filenames.
func DigestHex(digest string) string { return strings.TrimPrefix(digest, "sha256:") }

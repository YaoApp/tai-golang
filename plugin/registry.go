// Package plugin maintains the SDK-side Plugin registry and artifact store.
package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/yaoapp/tai-golang/pb"
)

var (
	ErrInvalidPlugin = errors.New("plugin_invalid")
	ErrNotFound      = errors.New("plugin_not_found")
	nameRE           = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
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
	data []byte
}

// New creates a registry. changed is called when the set changes.
func New(changed func()) *Registry {
	return &Registry{plugins: map[string]*stored{}, changed: changed}
}

// RegisterPlugin validates, hashes and stores a Plugin. Digest/Size are
// computed by the SDK when empty.
func (r *Registry) RegisterPlugin(entry *pb.PluginEntry) error {
	if entry == nil || !nameRE.MatchString(entry.Name) || !versionRE.MatchString(entry.Version) {
		return fmt.Errorf("%w: invalid name/version", ErrInvalidPlugin)
	}
	if entry.Runtime != "wasm" {
		return fmt.Errorf("%w: runtime %q", ErrInvalidPlugin, entry.Runtime)
	}
	if len(entry.Artifact) == 0 {
		return fmt.Errorf("%w: artifact is required", ErrInvalidPlugin)
	}
	sum := sha256.Sum256(entry.Artifact)
	entry.Digest = "sha256:" + hex.EncodeToString(sum[:])
	entry.Size = uint64(len(entry.Artifact))
	key := entry.Name + "@" + entry.Version
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.plugins[key]; ok {
		return fmt.Errorf("%w: duplicate %s", ErrInvalidPlugin, key)
	}
	meta := cloneMeta(entry)
	r.plugins[key] = &stored{meta: meta, data: append([]byte(nil), entry.Artifact...)}
	r.notify()
	return nil
}

// UnregisterPlugin removes a Plugin.
func (r *Registry) UnregisterPlugin(name, version string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := name + "@" + version
	if _, ok := r.plugins[key]; !ok {
		return ErrNotFound
	}
	delete(r.plugins, key)
	r.notify()
	return nil
}

// SetRemotePlugins replaces the registry atomically.
func (r *Registry) SetRemotePlugins(entries []*pb.PluginEntry) error {
	next := make(map[string]*stored, len(entries))
	for _, e := range entries {
		if e == nil || !nameRE.MatchString(e.Name) || !versionRE.MatchString(e.Version) || e.Runtime != "wasm" || len(e.Artifact) == 0 {
			return fmt.Errorf("%w: invalid entry", ErrInvalidPlugin)
		}
		sum := sha256.Sum256(e.Artifact)
		e.Digest = "sha256:" + hex.EncodeToString(sum[:])
		e.Size = uint64(len(e.Artifact))
		key := e.Name + "@" + e.Version
		if _, ok := next[key]; ok {
			return fmt.Errorf("%w: duplicate %s", ErrInvalidPlugin, key)
		}
		next[key] = &stored{meta: cloneMeta(e), data: append([]byte(nil), e.Artifact...)}
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

// Artifact returns the bytes for a digest.
func (r *Registry) Artifact(digest string) ([]byte, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, s := range r.plugins {
		if s.meta.Digest == digest {
			return append([]byte(nil), s.data...), nil
		}
	}
	return nil, ErrNotFound
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

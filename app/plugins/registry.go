// Package plugins registers the Demo App Wasm plugins.
package plugins

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yaoapp/tai-golang/pb"
)

// Registrar is the SDK Plugin API used by the demo.
type Registrar interface {
	RegisterPlugin(*pb.PluginEntry) error
}

// meta is plugin.json.
type meta struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Runtime      string   `json:"runtime"`
	Capabilities struct{} `json:"capabilities"`
}

//go:embed demo/*/plugin.json
var metaFS embed.FS

// Register loads demo.normalize/demo.tag artifacts from wasmDir.
func Register(reg Registrar, wasmDir string) error {
	if wasmDir == "" {
		return nil
	}
	for _, name := range []string{"normalize", "tag", "bad_fetch"} {
		dir := filepath.Join("demo", name)
		metaData, err := metaFS.ReadFile(filepath.Join(dir, "plugin.json"))
		if err != nil {
			return fmt.Errorf("plugins: read %s: %w", name, err)
		}
		var m meta
		if err := json.Unmarshal(metaData, &m); err != nil {
			return fmt.Errorf("plugins: decode %s: %w", name, err)
		}
		artifactPath := filepath.Join(wasmDir, name+".wasm")
		if _, err := os.Stat(artifactPath); err != nil {
			return fmt.Errorf("plugins: artifact %s: %w", artifactPath, err)
		}
		entry := &pb.PluginEntry{Name: m.Name, Version: m.Version, Runtime: m.Runtime, ArtifactPath: artifactPath}
		if err := reg.RegisterPlugin(entry); err != nil {
			return err
		}
	}
	return nil
}

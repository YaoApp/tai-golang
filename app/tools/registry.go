// Package tools registers all Remote Tools exposed by the Demo App.
package tools

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yaoapp/tai-golang/app/tools/demo"
	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/remotetool"
)

//go:embed demo/*_schema.json
var schemas embed.FS

// Registrar is the SDK Remote Tool API used by the demo.
type Registrar interface {
	SetRemoteTools([]*remotetool.Tool) error
}

type spec struct {
	Name        string          `json:"name"`
	Group       string          `json:"group"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Pipeline    []*pb.Step      `json:"pipeline"`
}

// Register loads embedded schemas and registers all handlers.
func Register(reg Registrar) error {
	handlers := demo.Tools()
	entries, err := schemas.ReadDir("demo")
	if err != nil {
		return err
	}
	var tools []*remotetool.Tool
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_schema.json") {
			continue
		}
		data, err := schemas.ReadFile("demo/" + e.Name())
		if err != nil {
			return err
		}
		var s spec
		if err := json.Unmarshal(data, &s); err != nil {
			return fmt.Errorf("tools: decode %s: %w", e.Name(), err)
		}
		h, ok := handlers[s.Name]
		if !ok {
			return fmt.Errorf("tools: no handler for %s", s.Name)
		}
		tool := &remotetool.Tool{Entry: &pb.RemoteToolEntry{
			Name: s.Name, Group: s.Group, Description: s.Description,
			InputSchema: s.InputSchema, Pipeline: s.Pipeline,
		}, Handler: h}
		if s.Name == "demo.echo" {
			tool.ProgressHandler = demo.ProgressEcho()
		}
		tools = append(tools, tool)
	}
	return reg.SetRemoteTools(tools)
}

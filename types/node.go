package types

import (
	"context"

	"github.com/yaoapp/tai-golang/pb"
)

// Node represents a connected Tai node (data struct, no behavior).
type Node struct {
	ID                string        // Unique node identifier
	TaiID             string        // Server-assigned identifier
	Version           string        // Tai binary version
	Capabilities      []string      // e.g. ["session","sandbox:native","realtime"]
	ContainerRuntimes []NodeRuntime // Available container runtimes
	OS                string        // "darwin" | "linux" | "windows"
	Arch              string        // "amd64" | "arm64"
	CPUCores          uint32
	MemoryMB          uint64
	DiskFreeMB        uint64
	Online            bool
}

// NodeRuntime describes a container runtime available on a node.
type NodeRuntime struct {
	Name    string // "docker" | "podman" | "containerd" | "nsjail"
	Version string // e.g. "27.1.0"
	Socket  string // e.g. "/var/run/docker.sock"
}

// ControlWriter writes a control message to a node's Control stream.
type ControlWriter interface {
	WriteControl(msg *pb.Payload) error
}

// RemoteNode is a behavioral node object that wraps a Node data struct
// with workspace, session, and control operations.
type RemoteNode interface {
	Info() *Node
	Online() bool

	// Workspace CRUD
	OpenWorkspace(ctx context.Context, wsID string, branch ...string) (Workspace, error)
	CreateWorkspace(ctx context.Context, meta Meta) (Workspace, error)
	ListWorkspaces(ctx context.Context, opts ListOptions) ([]RegistryEntry, error)
	DeleteWorkspace(ctx context.Context, wsID string) error

	// Session lifecycle
	OpenSession(ctx context.Context, req *SessionOpenRequest) (Session, error)
	ResumeSession(ctx context.Context, req *ResumeSessionRequest) (Session, error)

	// Control
	SendControl(msg *ControlMsg) error
	GoAway(reason string, reconnectAfter uint32) error
}

// NodeRegistry is the registration interface consumed by transport/.
// transport/ uses this interface instead of importing node/ directly,
// breaking the circular dependency.
type NodeRegistry interface {
	Register(nodeID string, reg *pb.Register, auth AuthProvider) (*pb.RegisterAck, error)
	BindStream(nodeID string, ctrl ControlWriter)
	UnbindStream(nodeID string, ctrl ControlWriter)
	UpdatePing(nodeID string, ping *pb.Ping)
	SendControl(nodeID string, msg *pb.Payload) error
	PoolConfig() *pb.PoolConfig
}

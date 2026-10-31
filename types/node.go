package types

// Node represents a connected Tai node.
type Node struct {
	ID                string        // Unique node identifier
	TaiID             string        // Server-assigned identifier
	Version           string        // Tai binary version
	Capabilities      []string      // e.g. ["session","tunnel","realtime","container"]
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

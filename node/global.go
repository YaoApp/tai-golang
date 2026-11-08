package node

import "github.com/yaoapp/tai-golang/types"

var defaultRegistry *Registry

// SetDefault registers the global default registry.
func SetDefault(r *Registry) { defaultRegistry = r }

// Get returns a RemoteNode by ID from the default registry, or nil.
func Get(id string) types.RemoteNode {
	return defaultRegistry.RemoteNodeByID(id)
}

// List returns all RemoteNodes from the default registry.
func List() []types.RemoteNode {
	return defaultRegistry.AllRemoteNodes()
}

// OnRegister sets the callback invoked after a node registers.
func OnRegister(fn func(types.RemoteNode) error) {
	defaultRegistry.SetOnRegister(fn)
}

// OnDisconnect sets the callback invoked when a node disconnects.
func OnDisconnect(fn func(nodeID string)) {
	defaultRegistry.SetOnDisconnect(fn)
}

// Default returns the process-wide node registry.
func Default() *Registry { return defaultRegistry }

// Package node manages connected Tai nodes and provides the RemoteNode
// behavioral wrapper. The package exposes a default registry via Get/List.
package node

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
	"github.com/yaoapp/tai-golang/workspace"
)

// Registry tracks connected Tai nodes in memory.
// It implements types.NodeRegistry for consumption by transport/.
type Registry struct {
	mu           sync.RWMutex
	nodes        map[string]*managedNode
	pool         *pb.PoolConfig
	policy       *workspace.ChannelPolicy
	auth         types.AuthProvider
	onRegister   func(types.RemoteNode) error
	onDisconnect func(nodeID string)
}

// SetAuth installs the auth provider used to issue user credentials when the
// transport opens or resumes a session.
func (r *Registry) SetAuth(auth types.AuthProvider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.auth = auth
}

// Auth returns the configured auth provider (may be nil).
func (r *Registry) Auth() types.AuthProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.auth
}

type managedNode struct {
	node     types.Node
	stream   types.ControlWriter
	lastSeen time.Time
}

// NewRegistry creates a Registry with the given pool configuration.
func NewRegistry(pool *pb.PoolConfig, policy *workspace.ChannelPolicy) *Registry {
	r := &Registry{
		nodes:  make(map[string]*managedNode),
		pool:   pool,
		policy: policy,
	}
	SetDefault(r)
	return r
}

// ---------- types.NodeRegistry implementation ----------

// Register handles a Register message, stores the node, and returns a RegisterAck.
func (r *Registry) Register(nodeID string, reg *pb.Register, auth types.AuthProvider) (*pb.RegisterAck, error) {
	taiID := generateTaiID()
	keyID, pubKey := auth.SigningPublicKey()

	node := types.Node{
		ID:           nodeID,
		TaiID:        taiID,
		Version:      reg.Version,
		Capabilities: reg.Capabilities,
		OS:           reg.Hardware.GetOs(),
		Arch:         reg.Hardware.GetArch(),
		CPUCores:     reg.Hardware.GetCpuCores(),
		MemoryMB:     reg.Hardware.GetMemoryMb(),
		DiskFreeMB:   reg.Hardware.GetDiskFreeMb(),
		Online:       true,
	}

	for _, cr := range reg.ContainerRuntimes {
		node.ContainerRuntimes = append(node.ContainerRuntimes, types.NodeRuntime{
			Name:    cr.Name,
			Version: cr.Version,
			Socket:  cr.Socket,
		})
	}

	r.mu.Lock()
	r.nodes[nodeID] = &managedNode{
		node:     node,
		lastSeen: time.Now(),
	}
	r.mu.Unlock()

	// Invoke onRegister callback with a RemoteNode wrapper.
	r.mu.RLock()
	onReg := r.onRegister
	r.mu.RUnlock()
	if onReg != nil {
		rn := r.wrapNode(nodeID)
		if err := onReg(rn); err != nil {
			r.mu.Lock()
			delete(r.nodes, nodeID)
			r.mu.Unlock()
			return nil, err
		}
	}

	ack := &pb.RegisterAck{
		TaiId:      taiID,
		PublicKey:  pubKey,
		KeyId:      keyID,
		PoolConfig: r.PoolConfig(),
	}
	return ack, nil
}

// BindStream associates a ControlWriter with an already-registered node.
func (r *Registry) BindStream(nodeID string, ctrl types.ControlWriter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if mn, ok := r.nodes[nodeID]; ok {
		mn.stream = ctrl
	}
}

// UnbindStream removes a node only while ctrl is still the active stream.
func (r *Registry) UnbindStream(nodeID string, ctrl types.ControlWriter) {
	r.mu.Lock()
	var removed bool
	if mn, ok := r.nodes[nodeID]; ok && mn.stream == ctrl {
		delete(r.nodes, nodeID)
		removed = true
	}
	onDisc := r.onDisconnect
	r.mu.Unlock()

	if removed && onDisc != nil {
		onDisc(nodeID)
	}
}

// UpdatePing updates liveness for a node.
func (r *Registry) UpdatePing(nodeID string, ping *pb.Ping) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if mn, ok := r.nodes[nodeID]; ok {
		mn.lastSeen = time.Now()
		_ = ping
	}
}

// SendControl sends a Payload to a specific node via its stream.
func (r *Registry) SendControl(nodeID string, msg *pb.Payload) error {
	r.mu.RLock()
	mn, ok := r.nodes[nodeID]
	r.mu.RUnlock()
	if !ok {
		return types.ErrNodeNotFound
	}
	if mn.stream == nil {
		return fmt.Errorf("node %s has no active stream", nodeID)
	}
	return mn.stream.WriteControl(msg)
}

// PoolConfig returns a copy of the pool parameters sent in register_ack.
func (r *Registry) PoolConfig() *pb.PoolConfig {
	return proto.Clone(r.pool).(*pb.PoolConfig)
}

// SetPoolConfig replaces the pool config.
func (r *Registry) SetPoolConfig(cfg *pb.PoolConfig) {
	r.mu.Lock()
	r.pool = cfg
	r.mu.Unlock()
}

// ---------- Node lookup ----------

// AllNodes returns all online nodes as data structs.
func (r *Registry) AllNodes() []*types.Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]*types.Node, 0, len(r.nodes))
	for _, mn := range r.nodes {
		n := mn.node
		result = append(result, &n)
	}
	return result
}

// NodeByID returns a node data struct by ID, or nil if not found.
func (r *Registry) NodeByID(nodeID string) *types.Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	mn, ok := r.nodes[nodeID]
	if !ok {
		return nil
	}
	n := mn.node
	return &n
}

// RemoteNodeByID returns a RemoteNode wrapper, or nil if not found.
func (r *Registry) RemoteNodeByID(nodeID string) types.RemoteNode {
	r.mu.RLock()
	_, ok := r.nodes[nodeID]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	return r.wrapNode(nodeID)
}

// AllRemoteNodes returns RemoteNode wrappers for all online nodes.
func (r *Registry) AllRemoteNodes() []types.RemoteNode {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]types.RemoteNode, 0, len(r.nodes))
	for id := range r.nodes {
		result = append(result, r.wrapNode(id))
	}
	return result
}

// ---------- Callbacks ----------

// SetOnRegister sets the callback invoked after a node registers.
func (r *Registry) SetOnRegister(fn func(types.RemoteNode) error) {
	r.mu.Lock()
	r.onRegister = fn
	r.mu.Unlock()
}

// SetOnDisconnect sets the callback invoked when a node disconnects.
func (r *Registry) SetOnDisconnect(fn func(nodeID string)) {
	r.mu.Lock()
	r.onDisconnect = fn
	r.mu.Unlock()
}

// ---------- GoAway helpers ----------

// GoAway sends a GoAway message to a specific node.
func (r *Registry) GoAway(nodeID string, reason string, reconnectAfter uint32) error {
	data, _ := marshalAny(&pb.GoAway{
		Reason:         reason,
		ReconnectAfter: reconnectAfter,
	})
	return r.SendControl(nodeID, &pb.Payload{
		Namespace: types.NamespaceControl,
		Kind:      "go_away",
		Data:      data,
	})
}

// GoAwayAll sends GoAway to all connected nodes.
func (r *Registry) GoAwayAll(reason string, reconnectAfter uint32) {
	r.mu.RLock()
	ids := make([]string, 0, len(r.nodes))
	for id := range r.nodes {
		ids = append(ids, id)
	}
	r.mu.RUnlock()

	for _, id := range ids {
		r.GoAway(id, reason, reconnectAfter)
	}
}

// wrapNode creates a remoteNode wrapper for the given nodeID.
func (r *Registry) wrapNode(nodeID string) *remoteNode {
	return &remoteNode{
		nodeID:   nodeID,
		registry: r,
		policy:   r.policy,
	}
}

// ---------- Helpers ----------

func generateTaiID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("tai_%x", b)
}

func marshalAny(m proto.Message) ([]byte, error) {
	return proto.Marshal(m)
}

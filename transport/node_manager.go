package transport

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
)

// NodeManager tracks connected Tai nodes in memory.
type NodeManager struct {
	mu         sync.RWMutex
	nodes      map[string]*managedNode
	onRegister func(node *types.Node) error
	pool       *pb.PoolConfig
}

type managedNode struct {
	node     types.Node
	stream   *controlStream
	lastSeen time.Time
}

// NewNodeManager creates a NodeManager with default pool configuration.
func NewNodeManager(pool *pb.PoolConfig) *NodeManager {
	return &NodeManager{
		nodes: make(map[string]*managedNode),
		pool:  pool,
	}
}

// PoolConfig returns a copy of the pool parameters sent in register_ack.
func (m *NodeManager) PoolConfig() *pb.PoolConfig {
	return proto.Clone(m.pool).(*pb.PoolConfig)
}

// SetPoolConfig replaces the pool config.
func (m *NodeManager) SetPoolConfig(cfg *pb.PoolConfig) {
	m.mu.Lock()
	m.pool = cfg
	m.mu.Unlock()
}

// Register handles a Register message, stores the node, and returns a RegisterAck.
func (m *NodeManager) Register(nodeID string, reg *pb.Register, auth types.AuthProvider) (*pb.RegisterAck, error) {
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

	m.mu.Lock()
	m.nodes[nodeID] = &managedNode{
		node:     node,
		lastSeen: time.Now(),
	}
	m.mu.Unlock()

	m.mu.RLock()
	onRegister := m.onRegister
	m.mu.RUnlock()
	if onRegister != nil {
		if err := onRegister(&node); err != nil {
			m.mu.Lock()
			delete(m.nodes, nodeID)
			m.mu.Unlock()
			return nil, err
		}
	}

	ack := &pb.RegisterAck{
		TaiId:      taiID,
		PublicKey:  pubKey,
		KeyId:      keyID,
		PoolConfig: m.PoolConfig(),
	}
	return ack, nil
}

// SetOnRegister sets the callback invoked after a node registers.
func (m *NodeManager) SetOnRegister(fn func(node *types.Node) error) {
	m.mu.Lock()
	m.onRegister = fn
	m.mu.Unlock()
}

// SetStream associates a controlStream with an already-registered node.
func (m *NodeManager) SetStream(nodeID string, cs *controlStream) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mn, ok := m.nodes[nodeID]; ok {
		mn.stream = cs
	}
}

// UnregisterStream removes a node only while cs is still the active stream.
func (m *NodeManager) UnregisterStream(nodeID string, cs *controlStream) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mn, ok := m.nodes[nodeID]; ok && mn.stream == cs {
		delete(m.nodes, nodeID)
	}
}

// Unregister removes a node.
func (m *NodeManager) Unregister(nodeID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.nodes, nodeID)
}

// AllNodes returns all online nodes.
func (m *NodeManager) AllNodes() []*types.Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*types.Node, 0, len(m.nodes))
	for _, mn := range m.nodes {
		n := mn.node
		result = append(result, &n)
	}
	return result
}

// NodeByID returns a node by ID, or nil if not found.
func (m *NodeManager) NodeByID(nodeID string) *types.Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	mn, ok := m.nodes[nodeID]
	if !ok {
		return nil
	}
	n := mn.node
	return &n
}

// UpdatePing updates liveness for a node.
func (m *NodeManager) UpdatePing(nodeID string, ping *pb.Ping) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mn, ok := m.nodes[nodeID]; ok {
		mn.lastSeen = time.Now()
		_ = ping
	}
}

// SendControl sends a Payload to a specific node via its stream.
func (m *NodeManager) SendControl(nodeID string, msg *pb.Payload) error {
	m.mu.RLock()
	mn, ok := m.nodes[nodeID]
	m.mu.RUnlock()
	if !ok {
		return types.ErrNodeNotFound
	}
	if mn.stream == nil {
		return fmt.Errorf("node %s has no active stream", nodeID)
	}
	return mn.stream.writeMsg(msg)
}

// GoAway sends a GoAway message to a node.
func (m *NodeManager) GoAway(nodeID string, reason string, reconnectAfter uint32) error {
	data, _ := marshalAny(&pb.GoAway{
		Reason:         reason,
		ReconnectAfter: reconnectAfter,
	})
	return m.SendControl(nodeID, &pb.Payload{Namespace: types.NamespaceControl, Kind: "go_away", Data: data})
}

// GoAwayAll sends GoAway to all connected nodes.
func (m *NodeManager) GoAwayAll(reason string, reconnectAfter uint32) {
	m.mu.RLock()
	ids := make([]string, 0, len(m.nodes))
	for id := range m.nodes {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	for _, id := range ids {
		m.GoAway(id, reason, reconnectAfter)
	}
}

func generateTaiID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("tai_%x", b)
}

func marshalAny(m proto.Message) ([]byte, error) {
	return proto.Marshal(m)
}

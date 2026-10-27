package tai

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/yaoapp/tai-golang/pb"
)

// nodeManager tracks connected Tai nodes in memory.
type nodeManager struct {
	mu         sync.RWMutex
	nodes      map[string]*managedNode
	onRegister func(node *Node) error
	pool       *pb.PoolConfig // register_ack pool_config; immutable after construction
}

// managedNode pairs a Node with its Control stream and liveness info.
type managedNode struct {
	node     Node
	stream   *controlStream
	lastSeen time.Time
}

func newNodeManager() *nodeManager {
	cfg, _ := (*PoolSettings)(nil).poolConfig() // defaults always validate
	return &nodeManager{
		nodes: make(map[string]*managedNode),
		pool:  cfg,
	}
}

// poolConfig returns a copy of the pool parameters sent in register_ack.
func (m *nodeManager) poolConfig() *pb.PoolConfig {
	return proto.Clone(m.pool).(*pb.PoolConfig)
}

// register handles a Register message, stores the node, and returns a RegisterAck.
func (m *nodeManager) register(nodeID string, reg *pb.Register, auth AuthProvider) (*pb.RegisterAck, error) {
	taiID := generateTaiID()
	keyID, pubKey := auth.SigningPublicKey()

	node := Node{
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
		node.ContainerRuntimes = append(node.ContainerRuntimes, NodeRuntime{
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
		PoolConfig: m.poolConfig(),
	}
	return ack, nil
}

// setOnRegister sets the callback invoked after a node registers; an error
// from it rejects the registration.
func (m *nodeManager) setOnRegister(fn func(node *Node) error) {
	m.mu.Lock()
	m.onRegister = fn
	m.mu.Unlock()
}

// setStream associates a controlStream with an already-registered node.
func (m *nodeManager) setStream(nodeID string, cs *controlStream) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mn, ok := m.nodes[nodeID]; ok {
		mn.stream = cs
	}
}

// unregisterStream removes a node only while cs is still the stream that
// registered it. A reconnected node has a newer stream; the older stream's
// teardown must not remove the new registration.
func (m *nodeManager) unregisterStream(nodeID string, cs *controlStream) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mn, ok := m.nodes[nodeID]; ok && mn.stream == cs {
		delete(m.nodes, nodeID)
	}
}

// unregister removes a node.
func (m *nodeManager) unregister(nodeID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.nodes, nodeID)
}

// allNodes returns all online nodes.
func (m *nodeManager) allNodes() []*Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Node, 0, len(m.nodes))
	for _, mn := range m.nodes {
		n := mn.node
		result = append(result, &n)
	}
	return result
}

// nodeByID returns a node by ID, or nil if not found.
func (m *nodeManager) nodeByID(nodeID string) *Node {
	m.mu.RLock()
	defer m.mu.RUnlock()
	mn, ok := m.nodes[nodeID]
	if !ok {
		return nil
	}
	n := mn.node
	return &n
}

// updatePing updates liveness and lightweight status for a node.
func (m *nodeManager) updatePing(nodeID string, ping *pb.Ping) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mn, ok := m.nodes[nodeID]; ok {
		mn.lastSeen = time.Now()
		_ = ping // session_count and cpu_percent can be stored if needed
	}
}

// sendControl sends a Payload to a specific node via its stream.
func (m *nodeManager) sendControl(nodeID string, msg *pb.Payload) error {
	m.mu.RLock()
	mn, ok := m.nodes[nodeID]
	m.mu.RUnlock()
	if !ok {
		return ErrNodeNotFound
	}
	if mn.stream == nil {
		return fmt.Errorf("node %s has no active stream", nodeID)
	}
	return mn.stream.writeMsg(msg)
}

// goAway sends a GoAway message to a node.
func (m *nodeManager) goAway(nodeID string, reason string, reconnectAfter uint32) error {
	data, _ := marshalAny(&pb.GoAway{
		Reason:         reason,
		ReconnectAfter: reconnectAfter,
	})
	return m.sendControl(nodeID, &pb.Payload{Namespace: NamespaceControl, Kind: "go_away", Data: data})
}

// goAwayAll sends GoAway to all connected nodes.
func (m *nodeManager) goAwayAll(reason string, reconnectAfter uint32) {
	m.mu.RLock()
	ids := make([]string, 0, len(m.nodes))
	for id := range m.nodes {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	for _, id := range ids {
		m.goAway(id, reason, reconnectAfter)
	}
}

func generateTaiID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("tai_%x", b)
}

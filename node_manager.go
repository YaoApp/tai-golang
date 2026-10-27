package tai

import (
	"crypto/rand"
	"fmt"
	"sync"
	"time"

	"github.com/yaoapp/tai-golang/pb"
)

// nodeManager tracks connected Tai nodes in memory.
type nodeManager struct {
	mu         sync.RWMutex
	nodes      map[string]*managedNode
	onRegister func(node *Node) error
}

// managedNode pairs a Node with its Control stream and liveness info.
type managedNode struct {
	node     Node
	stream   *controlStream
	lastSeen time.Time
}

func newNodeManager() *nodeManager {
	return &nodeManager{
		nodes: make(map[string]*managedNode),
	}
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

	if m.onRegister != nil {
		if err := m.onRegister(&node); err != nil {
			m.mu.Lock()
			delete(m.nodes, nodeID)
			m.mu.Unlock()
			return nil, err
		}
	}

	ack := &pb.RegisterAck{
		TaiId:     taiID,
		PublicKey: pubKey,
		KeyId:     keyID,
		PoolConfig: &pb.PoolConfig{
			LightMinConns:       2,
			LightMaxConns:       8,
			LightMaxStreams:     256,
			LightIdleTimeoutSec: 300,
			HeavyMinIdle:        2,
			HeavyMaxActive:      20,
			HeavyMaxIdle:        6,
			HeavyIdleTimeoutSec: 300,
			HeavyHealthCheckSec: 30,
		},
	}
	return ack, nil
}

// setStream associates a controlStream with an already-registered node.
func (m *nodeManager) setStream(nodeID string, cs *controlStream) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mn, ok := m.nodes[nodeID]; ok {
		mn.stream = cs
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

// sendControl sends a ControlMessage to a specific node via its stream.
func (m *nodeManager) sendControl(nodeID string, msg *pb.ControlMessage) error {
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
	return m.sendControl(nodeID, &pb.ControlMessage{Kind: "go_away", Data: data})
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

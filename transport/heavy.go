package transport

import (
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// heavyPool caches authorized Heavy connections from Tai nodes.
// Each Heavy connection carries one transfer at a time (connection-level
// exclusive, no multiplexing).
type heavyPool struct {
	mu     sync.Mutex
	conns  map[string][]*heavyConn // nodeID → connections
	cfg    *pb.PoolConfig
	reg    types.NodeRegistry
	auth   types.AuthProvider
	done   chan struct{}
	closed bool
}

// heavyConn wraps a single Heavy TCP connection.
type heavyConn struct {
	conn   net.Conn
	nodeID string
	idle   bool
	done   chan struct{} // closed when this conn is removed from the pool
}

func newHeavyPool(cfg *pb.PoolConfig, reg types.NodeRegistry, auth types.AuthProvider) *heavyPool {
	return &heavyPool{
		conns: make(map[string][]*heavyConn),
		cfg:   cfg,
		reg:   reg,
		auth:  auth,
		done:  make(chan struct{}),
	}
}

// Accept validates a Heavy TCP connection from Tai and caches it.
// Blocks until the connection is removed from the pool or the pool closes.
// Called from handleTaiConn when ConnectType == "heavy".
func (p *heavyPool) Accept(conn net.Conn, nodeID string) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		conn.Close()
		return
	}
	p.mu.Unlock()

	if err := WriteProtoMsg(conn, &pb.ConnectAck{}, maxConnectFrameSize); err != nil {
		log.Printf("tai-sdk: Heavy write connect_ack: %v", err)
		conn.Close()
		return
	}

	done := make(chan struct{})
	hc := &heavyConn{
		conn:   conn,
		nodeID: nodeID,
		idle:   true,
		done:   done,
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		conn.Close()
		return
	}
	p.conns[nodeID] = append(p.conns[nodeID], hc)
	p.mu.Unlock()

	log.Printf("tai-sdk: Heavy connection cached for node %s", nodeID)

	// Block until removed or pool closes, keeping serveTai alive.
	select {
	case <-done:
	case <-p.done:
	}
	log.Printf("tai-sdk: Heavy connection released for node %s", nodeID)
}

// Pick returns an idle Heavy connection for nodeID, marking it busy.
// If no idle connection is available, it sends a pool.adjust request
// via Control and returns an error.
func (p *heavyPool) Pick(nodeID string) (net.Conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, hc := range p.conns[nodeID] {
		if hc.idle {
			hc.idle = false
			return hc.conn, nil
		}
	}

	count := len(p.conns[nodeID])
	go p.requestAdjust(nodeID, int32(count+1))

	return nil, fmt.Errorf("%w: no idle heavy connection for node %s", types.ErrNoHeavyConn, nodeID)
}

// Return marks a Heavy connection as idle after a transfer completes.
func (p *heavyPool) Return(conn net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, conns := range p.conns {
		for _, hc := range conns {
			if hc.conn == conn {
				hc.idle = true
				return
			}
		}
	}
}

// Remove removes a Heavy connection from the pool (e.g. on disconnect).
func (p *heavyPool) Remove(conn net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for nodeID, conns := range p.conns {
		for i, hc := range conns {
			if hc.conn == conn {
				p.conns[nodeID] = append(conns[:i], conns[i+1:]...)
				if len(p.conns[nodeID]) == 0 {
					delete(p.conns, nodeID)
				}
				close(hc.done)
				return
			}
		}
	}
}

// ConnCount returns the number of Heavy connections for a node.
func (p *heavyPool) ConnCount(nodeID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.conns[nodeID])
}

// IdleCount returns the number of idle Heavy connections for a node.
func (p *heavyPool) IdleCount(nodeID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for _, hc := range p.conns[nodeID] {
		if hc.idle {
			count++
		}
	}
	return count
}

// Close closes all Heavy connections and marks the pool as closed.
func (p *heavyPool) Close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	for _, conns := range p.conns {
		for _, hc := range conns {
			hc.conn.Close()
			select {
			case <-hc.done:
			default:
				close(hc.done)
			}
		}
	}
	p.conns = make(map[string][]*heavyConn)
	p.mu.Unlock()
	close(p.done)
}

func (p *heavyPool) requestAdjust(nodeID string, targetSize int32) {
	data, _ := proto.Marshal(&pb.PoolAdjust{
		Pool:       "heavy",
		TargetSize: uint32(targetSize),
	})
	if err := p.reg.SendControl(nodeID, &pb.Payload{
		Namespace: types.NamespaceControl,
		Kind:      "pool.adjust",
		Data:      data,
	}); err != nil {
		log.Printf("tai-sdk: heavy pool.adjust for %s: %v", nodeID, err)
	}
}

func (p *heavyPool) updateConfig(cfg *pb.PoolConfig) {
	p.mu.Lock()
	p.cfg = cfg
	p.mu.Unlock()
}

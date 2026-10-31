package mock

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/transport"
)

// MockHeavyConn wraps a Heavy TCP connection from MockNode to the SDK server.
type MockHeavyConn struct {
	Conn net.Conn
}

// Close closes the Heavy connection.
func (hc *MockHeavyConn) Close() {
	if hc.Conn != nil {
		hc.Conn.Close()
	}
}

// ConnectHeavy opens a Heavy TCP connection to the SDK server.
// It dials TCP, sends a Connect frame with connect_type:"heavy",
// and reads the ConnectAck.
func (m *MockNode) ConnectHeavy() (*MockHeavyConn, error) {
	if m.nodeToken == "" {
		return nil, fmt.Errorf("nodeToken not set")
	}
	if m.h2Addr == "" {
		return nil, fmt.Errorf("h2Addr not set")
	}

	wk, _ := m.Discover()
	var conn net.Conn
	if wk != nil && wk.TLS != nil && wk.TLS.CACertificate != "" {
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM([]byte(wk.TLS.CACertificate))
		var err error
		conn, err = tls.Dial("tcp", m.h2Addr, &tls.Config{
			RootCAs:    pool,
			NextProtos: []string{transport.AlpnTai},
		})
		if err != nil {
			return nil, fmt.Errorf("dial heavy TLS: %w", err)
		}
	} else {
		var err error
		conn, err = net.Dial("tcp", m.h2Addr)
		if err != nil {
			return nil, fmt.Errorf("dial heavy: %w", err)
		}
	}

	connectMsg := &pb.Connect{
		NodeId:      m.ID,
		NodeToken:   m.nodeToken,
		ConnectType: transport.ConnTypeHeavy,
	}
	if err := transport.WriteProtoMsg(conn, connectMsg, 4*1024); err != nil {
		conn.Close()
		return nil, fmt.Errorf("write connect: %w", err)
	}

	var ack pb.ConnectAck
	if err := transport.ReadProtoMsg(conn, &ack, 4*1024); err != nil {
		conn.Close()
		return nil, fmt.Errorf("read connect_ack: %w", err)
	}

	return &MockHeavyConn{Conn: conn}, nil
}

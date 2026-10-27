package tai

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/quic-go/quic-go/http3"
	"github.com/yaoapp/tai-golang/pb"
	"google.golang.org/protobuf/proto"
)

// MockNode simulates a Tai client for SDK self-contained testing.
// It supports both high-level Start/Stop and low-level step-by-step APIs.
type MockNode struct {
	// ID is the unique node identifier.
	ID string
	// Hardware is the node hardware info sent in register.
	Hardware *pb.Hardware
	// Runtimes is the list of container runtimes.
	Runtimes []*pb.ContainerRuntime
	// Caps is the capability list (e.g. "session", "tunnel").
	Caps []string
	// Version is the Tai binary version string.
	Version string

	serverURL  string // HTTP API base URL (http://host:port)
	h2Addr     string // H2 transport address (host:port, TCP)
	h3Addr     string // H3 transport address (host:port, UDP)
	nodeToken  string
	taiID      string
	poolConfig *pb.PoolConfig
	publicKey  []byte
	keyID      string

	codec *FrameCodec

	// Control stream
	mu         sync.Mutex
	controlR   io.ReadCloser
	controlW   io.Writer
	controlF   http.Flusher
	httpResp   *http.Response
	httpClient *http.Client
	protocol   string          // "H3" or "H2"
	pendingCh  chan httpResult // set by Connect, consumed by Register

	// Lifecycle
	registered chan struct{}
	pingCancel context.CancelFunc
	recvCancel context.CancelFunc
	onControl  func(*pb.ControlMessage)
	stopped    bool
}

// NewMockNode creates a MockNode for testing.
// h2Addr is the H2 transport (TCP) address; h3Addr is the H3 transport (QUIC) address.
func NewMockNode(id string, serverHTTPURL string, h2Addr string, h3Addr string) *MockNode {
	return &MockNode{
		ID:        id,
		Version:   "0.1.0-mock",
		serverURL: serverHTTPURL,
		h2Addr:    h2Addr,
		h3Addr:    h3Addr,
		codec:     defaultCodec,
		Hardware: &pb.Hardware{
			Os:         "linux",
			Arch:       "amd64",
			CpuCores:   4,
			MemoryMb:   8192,
			DiskFreeMb: 50000,
		},
		Caps:       []string{"session"},
		registered: make(chan struct{}),
	}
}

// OnControl registers a handler called for each downstream Control message
// (e.g. go_away, key.rotation). Called from the recv goroutine.
func (m *MockNode) OnControl(fn func(*pb.ControlMessage)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onControl = fn
}

// Start runs the full connection flow: discover -> ticket -> connect -> register -> ping.
// It blocks until registration completes or ctx is cancelled.
func (m *MockNode) Start(ctx context.Context) error {
	wk, err := m.Discover()
	if err != nil {
		return fmt.Errorf("discover: %w", err)
	}

	ticket, err := m.ObtainTicket("control")
	if err != nil {
		return fmt.Errorf("obtain ticket: %w", err)
	}

	if err := m.Connect(ctx, ticket, wk.Protocols); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	if _, err := m.Register(); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	m.StartPing(ctx, 30*time.Second)
	m.startRecv(ctx)

	return nil
}

// WaitRegistered blocks until register_ack is received or timeout expires.
func (m *MockNode) WaitRegistered(timeout time.Duration) error {
	select {
	case <-m.registered:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("registration timeout after %v", timeout)
	}
}

// Stop closes the Control stream and cancels background goroutines.
func (m *MockNode) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return
	}
	m.stopped = true
	if m.pingCancel != nil {
		m.pingCancel()
	}
	if m.recvCancel != nil {
		m.recvCancel()
	}
	if m.controlW != nil {
		if closer, ok := m.controlW.(io.Closer); ok {
			closer.Close()
		}
	}
	if m.controlR != nil {
		m.controlR.Close()
	}
	if m.httpResp != nil {
		m.httpResp.Body.Close()
	}
}

// TaiID returns the server-assigned tai_id after registration.
func (m *MockNode) TaiID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.taiID
}

// PoolConfig returns the pool configuration received from the server.
func (m *MockNode) PoolConfig() *pb.PoolConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.poolConfig
}

// Protocol returns "H3" or "H2" depending on which transport was used.
func (m *MockNode) Protocol() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.protocol
}

// ---------------------------------------------------------------------------
// Low-level API
// ---------------------------------------------------------------------------

// Discover calls GET /.well-known/yao-transport and returns the response.
func (m *MockNode) Discover() (*WellKnownResponse, error) {
	resp, err := http.Get(m.serverURL + "/.well-known/yao-transport")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("well-known: status %d", resp.StatusCode)
	}

	var wk WellKnownResponse
	if err := json.NewDecoder(resp.Body).Decode(&wk); err != nil {
		return nil, fmt.Errorf("decode well-known: %w", err)
	}
	return &wk, nil
}

// ObtainTicket calls POST /v1/tai/connect and returns the ticket string.
// The nodeToken must be set before calling this (set automatically if Start is used).
func (m *MockNode) ObtainTicket(connType string) (string, error) {
	if m.nodeToken == "" {
		token, err := m.issueTokenFromServer()
		if err != nil {
			return "", fmt.Errorf("issue node_token: %w", err)
		}
		m.nodeToken = token
	}

	body, _ := json.Marshal(map[string]string{
		"node_id":      m.ID,
		"connect_type": connType,
	})

	req, _ := http.NewRequest("POST", m.serverURL+"/v1/tai/connect", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+m.nodeToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("connect: status %d", resp.StatusCode)
	}

	var cr connectResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return "", fmt.Errorf("decode connect response: %w", err)
	}
	return cr.ConnectTicket, nil
}

// Connect establishes an H3 or H2 transport connection.
// protocols should be ordered by preference (H3 first). The ticket authenticates the connection.
// Each protocol attempt uses a fresh pipe, so fallback works even if the first attempt fails.
func (m *MockNode) Connect(ctx context.Context, ticket string, protocols []string) error {
	var lastErr error
	for _, p := range protocols {
		switch p {
		case "HTTP/3":
			if m.h3Addr == "" {
				continue
			}
			if err := m.connectH3(ctx, ticket); err != nil {
				log.Printf("mocknode: H3 connect failed: %v, trying next protocol", err)
				lastErr = err
				continue
			}
			return nil
		case "HTTP/2":
			if m.h2Addr == "" {
				continue
			}
			if err := m.connectH2(ctx, ticket); err != nil {
				lastErr = err
				continue
			}
			return nil
		}
	}
	if lastErr != nil {
		return fmt.Errorf("all protocols failed, last: %w", lastErr)
	}
	return fmt.Errorf("no supported protocol in %v", protocols)
}

// Register sends register and reads register_ack.
// Must be called after Connect. The first call also finalizes the HTTP response
// (the server sends 200 OK after receiving and processing the register frame).
func (m *MockNode) Register() (*pb.RegisterAck, error) {
	reg := &pb.Register{
		NodeId:            m.ID,
		Version:           m.Version,
		Hardware:          m.Hardware,
		Capabilities:      m.Caps,
		ContainerRuntimes: m.Runtimes,
	}

	regData, err := proto.Marshal(reg)
	if err != nil {
		return nil, fmt.Errorf("marshal register: %w", err)
	}

	// Write register frame to the pipe. This triggers the server to respond.
	if err := m.SendControl(&pb.ControlMessage{Kind: "register", Data: regData}); err != nil {
		return nil, fmt.Errorf("send register: %w", err)
	}

	// Await the HTTP response if not yet received (first call after Connect).
	if err := m.ensureControlReader(); err != nil {
		return nil, fmt.Errorf("control stream: %w", err)
	}

	msg, err := m.RecvControl()
	if err != nil {
		return nil, fmt.Errorf("recv register_ack: %w", err)
	}
	if msg.Kind != "register_ack" {
		return nil, fmt.Errorf("expected register_ack, got %q", msg.Kind)
	}

	ack := &pb.RegisterAck{}
	if err := proto.Unmarshal(msg.Data, ack); err != nil {
		return nil, fmt.Errorf("unmarshal register_ack: %w", err)
	}

	m.mu.Lock()
	m.taiID = ack.TaiId
	m.poolConfig = ack.PoolConfig
	m.publicKey = ack.PublicKey
	m.keyID = ack.KeyId
	m.mu.Unlock()

	// Signal registration complete
	select {
	case <-m.registered:
	default:
		close(m.registered)
	}

	return ack, nil
}

// ensureControlReader waits for the pending HTTP response if it hasn't arrived yet.
func (m *MockNode) ensureControlReader() error {
	m.mu.Lock()
	ch := m.pendingCh
	m.pendingCh = nil
	m.mu.Unlock()

	if ch == nil {
		return nil // already received
	}

	res := <-ch
	if res.err != nil {
		return res.err
	}
	if res.resp.StatusCode != http.StatusOK {
		res.resp.Body.Close()
		return fmt.Errorf("status %d", res.resp.StatusCode)
	}
	m.mu.Lock()
	m.controlR = res.resp.Body
	m.httpResp = res.resp
	m.mu.Unlock()
	return nil
}

// StartPing starts a background goroutine that sends pings at the given interval.
func (m *MockNode) StartPing(ctx context.Context, interval time.Duration) {
	pingCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.pingCancel = cancel
	m.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-ticker.C:
				ping := &pb.Ping{Timestamp: time.Now().Unix()}
				pingData, _ := proto.Marshal(ping)
				m.SendControl(&pb.ControlMessage{Kind: "ping", Data: pingData})
			}
		}
	}()
}

// SendControl writes a ControlMessage to the transport stream.
func (m *MockNode) SendControl(msg *pb.ControlMessage) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.controlW == nil {
		return fmt.Errorf("not connected")
	}
	if err := m.codec.WriteFrame(m.controlW, msg); err != nil {
		return err
	}
	if m.controlF != nil {
		m.controlF.Flush()
	}
	return nil
}

// RecvControl reads the next ControlMessage from the transport stream.
func (m *MockNode) RecvControl() (*pb.ControlMessage, error) {
	m.mu.Lock()
	r := m.controlR
	m.mu.Unlock()
	if r == nil {
		return nil, fmt.Errorf("not connected")
	}
	return m.codec.ReadFrame(r)
}

// RefreshToken calls POST /v1/tai/token/refresh with the current node_token.
func (m *MockNode) RefreshToken() (string, error) {
	body, _ := json.Marshal(refreshRequest{NodeToken: m.nodeToken})
	resp, err := http.Post(m.serverURL+"/v1/tai/token/refresh", "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("refresh: status %d", resp.StatusCode)
	}

	var rr refreshResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return "", err
	}
	m.nodeToken = rr.NodeToken
	return rr.NodeToken, nil
}

// ---------------------------------------------------------------------------
// Internal
// ---------------------------------------------------------------------------

// issueTokenFromServer creates a node_token via the AuthProvider.
// This works in test because TestServer.Auth is accessible.
func (m *MockNode) issueTokenFromServer() (string, error) {
	return "", fmt.Errorf("nodeToken not set; call SetNodeToken or use TestServer.IssueTestNodeToken")
}

// SetNodeToken sets the node_token for authentication.
func (m *MockNode) SetNodeToken(token string) {
	m.nodeToken = token
}

// httpResult pairs a response with its error from an async Do call.
type httpResult struct {
	resp *http.Response
	err  error
}

func (m *MockNode) connectH2(ctx context.Context, ticket string) error {
	pr, pw := io.Pipe()

	var h2URL string
	var client *http.Client

	wk, _ := m.Discover()
	if wk != nil && wk.TLS != nil && wk.TLS.CACertificate != "" {
		h2URL = fmt.Sprintf("https://%s/v1/tai/control", m.h2Addr)
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM([]byte(wk.TLS.CACertificate))
		client = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig:   &tls.Config{RootCAs: pool},
				ForceAttemptHTTP2: true,
			},
		}
	} else {
		h2URL = fmt.Sprintf("http://%s/v1/tai/control", m.h2Addr)
		client = m.h2cClient()
	}

	req, err := http.NewRequestWithContext(ctx, "POST", h2URL, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+ticket)
	req.Header.Set("Content-Type", "application/x-protobuf")

	ch := make(chan httpResult, 1)
	go func() {
		resp, err := client.Do(req)
		ch <- httpResult{resp, err}
	}()

	m.mu.Lock()
	m.controlW = pw
	m.httpClient = client
	m.protocol = "H2"
	m.pendingCh = ch
	m.mu.Unlock()

	return nil
}

func (m *MockNode) connectH3(ctx context.Context, ticket string) error {
	h3URL := fmt.Sprintf("https://%s/v1/tai/control", m.h3Addr)

	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, "POST", h3URL, pr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+ticket)
	req.Header.Set("Content-Type", "application/x-protobuf")

	client := m.h3Client()

	ch := make(chan httpResult, 1)
	go func() {
		resp, err := client.Do(req)
		ch <- httpResult{resp, err}
	}()

	m.mu.Lock()
	m.controlW = pw
	m.httpClient = client
	m.protocol = "H3"
	m.pendingCh = ch
	m.mu.Unlock()

	return nil
}

func (m *MockNode) startRecv(ctx context.Context) {
	recvCtx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.recvCancel = cancel
	m.mu.Unlock()

	go func() {
		for {
			select {
			case <-recvCtx.Done():
				return
			default:
			}

			msg, err := m.RecvControl()
			if err != nil {
				if err != io.EOF {
					log.Printf("mocknode: recv error: %v", err)
				}
				return
			}

			m.mu.Lock()
			handler := m.onControl
			m.mu.Unlock()

			switch msg.Kind {
			case "pong":
				pong := &pb.Pong{}
				if proto.Unmarshal(msg.Data, pong) == nil && pong.PoolConfig != nil {
					m.mu.Lock()
					m.poolConfig = pong.PoolConfig
					m.mu.Unlock()
				}
			default:
				if handler != nil {
					handler(msg)
				}
			}
		}
	}()
}

func (m *MockNode) h2cClient() *http.Client {
	var protos http.Protocols
	protos.SetHTTP1(false)
	protos.SetUnencryptedHTTP2(true)
	return &http.Client{
		Transport: &http.Transport{
			Protocols: &protos,
		},
	}
}

func (m *MockNode) h3Client() *http.Client {
	pool := x509.NewCertPool()
	wk, _ := m.Discover()
	if wk != nil && wk.TLS != nil && wk.TLS.CACertificate != "" {
		pool.AppendCertsFromPEM([]byte(wk.TLS.CACertificate))
	}

	return &http.Client{
		Transport: &http3.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs: pool,
			},
		},
	}
}

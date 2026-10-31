package mock

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
	"github.com/yaoapp/tai-golang/transport"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// MockNode simulates a Tai client for SDK self-contained testing.
type MockNode struct {
	ID       string
	Hardware *pb.Hardware
	Runtimes []*pb.ContainerRuntime
	Caps     []string
	Version  string

	serverURL  string
	h2Addr     string
	h3Addr     string
	nodeToken  string
	taiID      string
	poolConfig *pb.PoolConfig
	publicKey  []byte
	keyID      string

	codec *transport.FrameCodec

	mu         sync.Mutex
	controlR   io.ReadCloser
	controlW   io.Writer
	controlF   http.Flusher
	httpResp   *http.Response
	httpClient *http.Client
	protocol   string
	pendingCh  chan httpResult

	registered chan struct{}
	pingCancel context.CancelFunc
	recvCancel context.CancelFunc
	onControl  func(*pb.Payload)
	stopped    bool
}

type httpResult struct {
	resp *http.Response
	err  error
}

// NewMockNode creates a MockNode for testing.
func NewMockNode(id string, serverHTTPURL string, h2Addr string, h3Addr string) *MockNode {
	return &MockNode{
		ID:        id,
		Version:   "0.1.0-mock",
		serverURL: serverHTTPURL,
		h2Addr:    h2Addr,
		h3Addr:    h3Addr,
		codec:     transport.DefaultCodec(),
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

// OnControl registers a handler for downstream Control messages.
func (m *MockNode) OnControl(fn func(*pb.Payload)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onControl = fn
}

// Start runs the full connection flow: discover -> connect -> register -> ping.
func (m *MockNode) Start(ctx context.Context) error {
	wk, err := m.Discover()
	if err != nil {
		return fmt.Errorf("discover: %w", err)
	}

	if err := m.Connect(ctx, wk.Protocols); err != nil {
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

// Stop closes the Control stream and releases resources.
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
	if m.httpClient != nil {
		switch t := m.httpClient.Transport.(type) {
		case *http3.Transport:
			t.Close()
		case *http.Transport:
			t.CloseIdleConnections()
		}
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

// Protocol returns "H3" or "H2".
func (m *MockNode) Protocol() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.protocol
}

// ---------------------------------------------------------------------------
// Low-level API
// ---------------------------------------------------------------------------

// Discover calls GET /.well-known/yao-transport.
func (m *MockNode) Discover() (*types.WellKnownResponse, error) {
	resp, err := http.Get(m.serverURL + "/.well-known/yao-transport")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("well-known: status %d", resp.StatusCode)
	}

	var wk types.WellKnownResponse
	if err := json.NewDecoder(resp.Body).Decode(&wk); err != nil {
		return nil, fmt.Errorf("decode well-known: %w", err)
	}
	return &wk, nil
}

type connectRequest struct {
	NodeID      string `json:"node_id"`
	ConnectType string `json:"connect_type"`
}

// Connect establishes a Control connection.
func (m *MockNode) Connect(ctx context.Context, protocols []string) error {
	if m.nodeToken == "" {
		return fmt.Errorf("nodeToken not set; call SetNodeToken or use TestServer.IssueTestNodeToken")
	}
	var lastErr error
	for _, p := range protocols {
		switch p {
		case "HTTP/3":
			if m.h3Addr == "" {
				continue
			}
			if err := m.connectH3(ctx); err != nil {
				log.Printf("mocknode: H3 connect failed: %v, trying next protocol", err)
				lastErr = err
				continue
			}
			return nil
		case "HTTP/2":
			if m.h2Addr == "" {
				continue
			}
			if err := m.connectH2(ctx); err != nil {
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

	if err := m.SendControl(&pb.Payload{Namespace: types.NamespaceControl, Kind: "register", Data: regData}); err != nil {
		return nil, fmt.Errorf("send register: %w", err)
	}

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

	select {
	case <-m.registered:
	default:
		close(m.registered)
	}

	return ack, nil
}

func (m *MockNode) ensureControlReader() error {
	m.mu.Lock()
	ch := m.pendingCh
	m.pendingCh = nil
	m.mu.Unlock()

	if ch == nil {
		return nil
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

// StartPing starts background pings.
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
				m.SendControl(&pb.Payload{Namespace: types.NamespaceControl, Kind: "ping", Data: pingData})
			}
		}
	}()
}

// SendControl writes a Payload to the transport stream.
func (m *MockNode) SendControl(msg *pb.Payload) error {
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

// RecvControl reads the next Payload from the transport stream.
func (m *MockNode) RecvControl() (*pb.Payload, error) {
	m.mu.Lock()
	r := m.controlR
	m.mu.Unlock()
	if r == nil {
		return nil, fmt.Errorf("not connected")
	}
	return m.codec.ReadFrame(r)
}

type refreshRequest struct {
	NodeToken string `json:"node_token"`
}

type refreshResponse struct {
	NodeToken string `json:"node_token"`
	ExpiresIn int    `json:"expires_in"`
}

// RefreshToken calls POST /v1/tai/token/refresh.
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

// SetNodeToken sets the node_token for authentication.
func (m *MockNode) SetNodeToken(token string) {
	m.nodeToken = token
}

// ---------------------------------------------------------------------------
// Internal
// ---------------------------------------------------------------------------

func closeClient(client *http.Client) {
	switch t := client.Transport.(type) {
	case *http3.Transport:
		t.Close()
	case *http.Transport:
		t.CloseIdleConnections()
	}
}

func (m *MockNode) authorize(ctx context.Context, client *http.Client, baseURL string) error {
	body, _ := json.Marshal(connectRequest{NodeID: m.ID, ConnectType: transport.ConnTypeControl})
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/tai/connect", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.nodeToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("connect: status %d", resp.StatusCode)
	}
	return nil
}

func (m *MockNode) connectH2(ctx context.Context) error {
	pr, pw := io.Pipe()

	var baseURL string
	var client *http.Client

	wk, _ := m.Discover()
	if wk != nil && wk.TLS != nil && wk.TLS.CACertificate != "" {
		baseURL = fmt.Sprintf("https://%s", m.h2Addr)
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM([]byte(wk.TLS.CACertificate))
		client = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig:   &tls.Config{RootCAs: pool},
				ForceAttemptHTTP2: true,
				MaxConnsPerHost:   1,
			},
		}
	} else {
		baseURL = fmt.Sprintf("http://%s", m.h2Addr)
		client = m.h2cClient()
	}

	if err := m.authorize(ctx, client, baseURL); err != nil {
		closeClient(client)
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/tai/control", pr)
	if err != nil {
		closeClient(client)
		return err
	}
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

func (m *MockNode) connectH3(ctx context.Context) error {
	baseURL := fmt.Sprintf("https://%s", m.h3Addr)
	client := m.h3Client()

	if err := m.authorize(ctx, client, baseURL); err != nil {
		closeClient(client)
		return err
	}

	pr, pw := io.Pipe()
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/v1/tai/control", pr)
	if err != nil {
		closeClient(client)
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")

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
			Protocols:       &protos,
			MaxConnsPerHost: 1,
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

// Package main implements the Tai SDK QA demo application.
// It starts a tai-golang SDK server and exposes an HTTP API + React frontend
// for manual end-to-end testing of node registration and sessions.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	tai "github.com/yaoapp/tai-golang"
	"github.com/yaoapp/tai-golang/auth"
)

func main() {
	var (
		apiPort       int
		transportPort int
		httpPort      int
	)
	flag.IntVar(&apiPort, "port", 3000, "API + frontend server port")
	flag.IntVar(&transportPort, "transport-port", 6099, "SDK transport port")
	flag.IntVar(&httpPort, "http-port", 5099, "SDK HTTP port (well-known, token refresh)")
	flag.Parse()

	// Generate ephemeral ed25519 key pair for auth.
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatalf("generate key: %v", err)
	}

	authProvider := auth.DefaultAuth(privateKey)

	// Create SDK server.
	sdkServer, err := tai.NewServer(&tai.ServerConfig{
		TransportPort: transportPort,
		HTTPPort:      httpPort,
		Auth:          authProvider,
		Pool: &tai.PoolSettings{
			LightMin:        1,
			LightMax:        8,
			LightMaxStreams: 256,
		},
	})
	if err != nil {
		log.Fatalf("create sdk server: %v", err)
	}

	// In-memory store for nodes/sessions.
	store := NewStore()

	// Register node callback.
	sdkServer.OnNodeRegister(func(node *tai.Node) error {
		store.AddNode(node)
		log.Printf("node registered: %s (tai_id=%s)", node.ID, node.TaiID)
		return nil
	})

	// Start SDK server in background.
	sdkCtx, sdkCancel := context.WithCancel(context.Background())
	defer sdkCancel()
	go func() {
		if err := sdkServer.Start(sdkCtx); err != nil {
			log.Printf("sdk server stopped: %v", err)
		}
	}()

	// Wait for SDK server to be ready.
	time.Sleep(200 * time.Millisecond)

	// Build API router.
	mux := NewAPI(sdkServer, authProvider, store)

	apiServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", apiPort),
		Handler: mux,
	}

	// Graceful shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
		sig := <-sigCh
		log.Printf("shutting down... (signal: %v)", sig)
		sdkCancel()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		apiServer.Shutdown(ctx)
	}()

	log.Printf("Demo app starting on http://localhost:%d", apiPort)
	log.Printf("SDK transport: :%d  HTTP: :%d", transportPort, httpPort)
	if err := apiServer.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("api server: %v", err)
	}
}

package session

import (
	"context"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

const (
	// user credentials are short lived (recommended TTL 30 minutes); renew
	// well before expiry so a long-running session never loses its credential.
	tokenRefreshInterval = 20 * time.Minute
)

// TokenRefresher periodically renews the session's user credential and pushes
// it to the Tai daemon via the L1 Control stream (kind "token.refresh"). The
// daemon stores the credential opaquely and does not validate it.
//
// Renewal follows the credential contract: the callback should re-sign the
// existing credential (RefreshUserToken inherits identity and scopes) and may
// fall back to a fresh IssueUserToken when the old one cannot be renewed.
type TokenRefresher struct {
	sessionID    string
	refreshToken func() (string, time.Time, error) // returns a fresh user token and its expiry (zero = unknown)
	sendControl  func(msg *types.ControlMsg) error // sends via L1 Control stream
	cancel       context.CancelFunc
}

// NewTokenRefresher starts a background goroutine that renews the user
// credential every 20 minutes.
func NewTokenRefresher(sessionID string, refreshToken func() (string, time.Time, error), sendControl func(msg *types.ControlMsg) error) *TokenRefresher {
	return newTokenRefresher(sessionID, refreshToken, sendControl, tokenRefreshInterval)
}

// newTokenRefresher is the internal constructor allowing a custom interval for testing.
func newTokenRefresher(sessionID string, refreshToken func() (string, time.Time, error), sendControl func(msg *types.ControlMsg) error, interval time.Duration) *TokenRefresher {
	ctx, cancel := context.WithCancel(context.Background())
	tr := &TokenRefresher{
		sessionID:    sessionID,
		refreshToken: refreshToken,
		sendControl:  sendControl,
		cancel:       cancel,
	}
	go tr.run(ctx, interval)
	return tr
}

// Stop stops the refresh goroutine.
func (tr *TokenRefresher) Stop() {
	tr.cancel()
}

func (tr *TokenRefresher) run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			token, expiresAt, err := tr.refreshToken()
			if err != nil {
				continue
			}
			var expiresUnix int64
			if !expiresAt.IsZero() {
				expiresUnix = expiresAt.Unix()
			}
			data, err := proto.Marshal(&pb.TokenRefresh{
				SessionId: tr.sessionID,
				Token:     token,
				ExpiresAt: expiresUnix,
			})
			if err != nil {
				continue
			}
			_ = tr.sendControl(&types.ControlMsg{
				Kind: "token.refresh",
				Data: data,
			})
		}
	}
}

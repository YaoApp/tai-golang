package session

import (
	"context"
	"time"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

const (
	tokenRefreshInterval = 20 * time.Minute // OperationToken valid for 30min; refresh at 20min
)

// TokenRefresher periodically refreshes the OperationToken for a long-running
// session via the L1 Control stream (not the session ctrl stream).
type TokenRefresher struct {
	sessionID   string
	issueToken  func() (string, error)            // issues a new OperationToken
	sendControl func(msg *types.ControlMsg) error // sends via L1 Control stream
	cancel      context.CancelFunc
}

// NewTokenRefresher starts a background goroutine that refreshes the
// OperationToken every 20 minutes.
func NewTokenRefresher(sessionID string, issueToken func() (string, error), sendControl func(msg *types.ControlMsg) error) *TokenRefresher {
	return newTokenRefresher(sessionID, issueToken, sendControl, tokenRefreshInterval)
}

// newTokenRefresher is the internal constructor allowing a custom interval for testing.
func newTokenRefresher(sessionID string, issueToken func() (string, error), sendControl func(msg *types.ControlMsg) error, interval time.Duration) *TokenRefresher {
	ctx, cancel := context.WithCancel(context.Background())
	tr := &TokenRefresher{
		sessionID:   sessionID,
		issueToken:  issueToken,
		sendControl: sendControl,
		cancel:      cancel,
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
			token, err := tr.issueToken()
			if err != nil {
				continue
			}
			data, err := proto.Marshal(&pb.TokenRefresh{
				SessionId: tr.sessionID,
				Token:     token,
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

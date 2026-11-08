package remotetool

import (
	"context"

	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
)

// Emitter forwards progress events for one call.
type Emitter func(*pb.RemoteToolEvent)

// RemoteToolHandler is the design Handler type with progress emission.
type RemoteToolHandler func(ctx context.Context, input []byte, emit Emitter) ([]byte, error)

type ctxKey struct{}

type callContext struct {
	callID    string
	user      *types.UserIdentity
	sessionID string
}

func withCallContext(ctx context.Context, callID string, user *types.UserIdentity, sessionID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, callContext{callID: callID, user: user, sessionID: sessionID})
}

// ToolCallID returns the SDK-assigned call id.
func ToolCallID(ctx context.Context) string { return fromContext(ctx).callID }

// User returns the verified caller identity.
func User(ctx context.Context) *types.UserIdentity { return fromContext(ctx).user }

// SessionID returns the optional session id.
func SessionID(ctx context.Context) string { return fromContext(ctx).sessionID }

func fromContext(ctx context.Context) callContext {
	if v, ok := ctx.Value(ctxKey{}).(callContext); ok {
		return v
	}
	return callContext{}
}

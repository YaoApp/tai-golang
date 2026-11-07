package types

import "github.com/yaoapp/tai-golang/pb"

// IdentityToPB converts a verified user identity into the wire form carried by
// the first frame of a business stream. expiresAt is a local GC hint (unix
// seconds); pass 0 when unknown.
func IdentityToPB(u *UserIdentity, token string, expiresAt int64) *pb.Identity {
	if u == nil && token == "" {
		return nil
	}
	wire := &pb.Identity{UserToken: token, ExpiresAt: expiresAt}
	if u != nil {
		wire.UserId = u.UserID
		wire.TeamId = u.TeamID
	}
	return wire
}

// IdentityFromPB converts the wire identity into the in-memory form.
func IdentityFromPB(wire *pb.Identity) *UserIdentity {
	if wire == nil {
		return nil
	}
	return &UserIdentity{UserID: wire.UserId, TeamID: wire.TeamId}
}

// DefaultSessionScopes is the scope set used when the caller does not declare
// any: enough to open and drive a session.
func DefaultSessionScopes() Scope { return Scope{SessionAccess} }

package transport

import (
	"github.com/yaoapp/tai-golang/pb"
)

// FirstFrame builds the first frame of a business stream (session open,
// resume, data, workspace, realtime sub-stream, ...).
//
// The caller identity is carried on the Payload envelope rather than inside the
// namespace-specific data, so every stream-opening site in the SDK must build
// its first frame through this helper: the identity cannot be dropped when a
// frame is reconstructed, and the wire contract stays the same for all
// namespaces.
//
// identity is optional. A nil identity means the stream represents no user
// (system task or unattended node); it is never an error.
func FirstFrame(namespace, kind string, data []byte, identity *pb.Identity) *pb.Payload {
	return &pb.Payload{
		Namespace: namespace,
		Kind:      kind,
		Data:      data,
		Identity:  identity,
	}
}

// IdentityFromFrame returns the caller identity carried by a business stream
// first frame, or nil when the frame carries none. Only the first frame of a
// stream is expected to carry the identity; later frames are empty by design.
func IdentityFromFrame(frame *pb.Payload) *pb.Identity {
	if frame == nil {
		return nil
	}
	return frame.Identity
}

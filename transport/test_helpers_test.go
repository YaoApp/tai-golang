package transport

import (
	"github.com/yaoapp/tai-golang/pb"
	"github.com/yaoapp/tai-golang/types"
	"google.golang.org/protobuf/proto"
)

// testNodeRegistry is a minimal types.NodeRegistry for unit tests.
type testNodeRegistry struct {
	pool *pb.PoolConfig
}

func (r *testNodeRegistry) Register(nodeID string, reg *pb.Register, auth types.AuthProvider) (*pb.RegisterAck, error) {
	return &pb.RegisterAck{
		TaiId:      "test-tai-id",
		PoolConfig: r.PoolConfig(),
	}, nil
}

func (r *testNodeRegistry) BindStream(nodeID string, ctrl types.ControlWriter) {}

func (r *testNodeRegistry) UnbindStream(nodeID string, ctrl types.ControlWriter) {}

func (r *testNodeRegistry) UpdatePing(nodeID string, ping *pb.Ping) {}

func (r *testNodeRegistry) SendControl(nodeID string, msg *pb.Payload) error {
	return nil
}

func (r *testNodeRegistry) PoolConfig() *pb.PoolConfig {
	return proto.Clone(r.pool).(*pb.PoolConfig)
}

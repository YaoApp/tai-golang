PROTO_DIR  := proto
PB_DIR     := pb
PROTO_FILES := $(wildcard $(PROTO_DIR)/*.proto)

.PHONY: proto test lint clean

# Generate Go code from .proto files
proto:
	@mkdir -p $(PB_DIR)
	protoc \
		--proto_path=$(PROTO_DIR) \
		--go_out=$(PB_DIR) \
		--go_opt=paths=source_relative \
		$(PROTO_FILES)
	@echo "protoc: generated $(PB_DIR)/*.pb.go"

# Run all tests
test:
	go test ./...

# Lint proto files (requires buf: https://buf.build)
lint:
	@command -v buf >/dev/null 2>&1 && buf lint $(PROTO_DIR) || echo "buf not installed, skipping proto lint"
	go vet ./...

# Remove generated files
clean:
	rm -f $(PB_DIR)/*.pb.go

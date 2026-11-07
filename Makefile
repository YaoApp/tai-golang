PROTO_DIR  := proto
PB_DIR     := pb
PROTO_FILES := $(wildcard $(PROTO_DIR)/*.proto)

.PHONY: proto test lint fmt vet clean

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

# Unit tests with coverage profile
.PHONY: cover
cover:
	go test ./... -timeout 600s -covermode=count -coverprofile=coverage.out
	@go tool cover -func=coverage.out | tail -1

# Per-package coverage gate (thresholds in scripts/coverage-thresholds.txt)
.PHONY: cover-gate
cover-gate: cover
	./scripts/coverage-gate.sh coverage.out --global 71

# Demo app module (separate go.mod)
.PHONY: cover-app
cover-app:
	cd app && go test ./... -timeout 600s -covermode=count -coverprofile=coverage.out

.PHONY: cover-all
cover-all: cover cover-app

# Race detector for the packages touched by the identity work
.PHONY: cover-race
cover-race:
	go test -race -timeout 900s ./...

# Format all Go source files
fmt:
	gofmt -w .

# Run go vet on all packages
vet:
	go vet ./...

# Lint: format check + vet + optional proto lint
lint:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "unformatted files (run make fmt):"; echo "$$unformatted"; exit 1; \
	fi
	go vet ./...
	@command -v buf >/dev/null 2>&1 && buf lint $(PROTO_DIR) || echo "buf not installed, skipping proto lint"

# Remove generated files
clean:
	rm -f $(PB_DIR)/*.pb.go

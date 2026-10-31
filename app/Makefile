.PHONY: build build-web dev clean

# Build frontend then Go binary
build: build-web
	go build -o tai-demo .

# Build React frontend
build-web:
	cd web && npm install && npm run build

# Run in development mode
dev:
	go run . --port 3000

# Run Vite dev server (separate terminal)
dev-web:
	cd web && npm run dev

# Clean build artifacts
clean:
	rm -f tai-demo
	rm -rf web/dist web/node_modules

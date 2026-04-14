.PHONY: build run clean test help

# Build variables
BIN_DIR := bin
BIN_NAME := torrent-client
BIN_PATH := $(BIN_DIR)/$(BIN_NAME)
CMD_PATH := ./cmd/torrent-client

help:
	@echo "BitTorrent Client - Makefile Commands"
	@echo "===================================="
	@echo "make build      - Build the binary"
	@echo "make run        - Build and run with big-buck-bunny.torrent"
	@echo "make test       - Run tests (if any)"
	@echo "make clean      - Clean build artifacts"
	@echo "make fmt        - Format code"
	@echo "make lint       - Lint code"

build:
	@echo "Building torrent-client..."
	@mkdir -p $(BIN_DIR)
	@go build -o $(BIN_PATH) $(CMD_PATH)
	@echo "✓ Built: $(BIN_PATH)"

run: build
	@echo "Running with big-buck-bunny.torrent..."
	@$(BIN_PATH) big-buck-bunny.torrent

run-magnet: build
	@echo "Running with magnet link..."
	@$(BIN_PATH) "$$(cat magnets.txt | head -1)"

clean:
	@echo "Cleaning..."
	@rm -rf $(BIN_DIR)
	@go clean
	@echo "✓ Cleaned"

fmt:
	@echo "Formatting code..."
	@go fmt ./...
	@echo "✓ Formatted"

lint:
	@echo "Linting code..."
	@go vet ./...
	@echo "✓ Linting complete"

test:
	@echo "Running tests..."
	@go test -v ./...

install-deps:
	@echo "Installing dependencies..."
	@go mod download
	@echo "✓ Dependencies installed"

verify:
	@echo "Verifying binary..."
	@file $(BIN_PATH)
	@$(BIN_PATH)

.DEFAULT_GOAL := help

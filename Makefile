# Makefile for Task Organizer

# 変数定義
BINARY_NAME := taskorganizer
GO := go
GOFLAGS := -v
BUILD_DIR := build
SRC_DIR := src
MAIN_GO := $(SRC_DIR)/cmd/main.go
INSTALL_DIR := /usr/local/bin

# OS/アーキテクチャ検出
GOOS := $(shell go env GOOS)
GOARCH := $(shell go env GOARCH)

# ビルド出力ファイル名
OUTPUT := $(BUILD_DIR)/$(BINARY_NAME)
ifeq ($(GOOS),windows)
	OUTPUT := $(BUILD_DIR)/$(BINARY_NAME).exe
endif

# デフォルトターゲット
.PHONY: all
all: build

# ビルドディレクトリ作成
$(BUILD_DIR):
	mkdir -p $(BUILD_DIR)

# ビルド
.PHONY: build
build: $(BUILD_DIR)
	@echo "Building $(BINARY_NAME) for $(GOOS)/$(GOARCH)..."
	cd $(SRC_DIR) && $(GO) build $(GOFLAGS) -o ../$(OUTPUT) ./cmd

# インストール
.PHONY: install
install: build
	@echo "Installing $(BINARY_NAME) to $(INSTALL_DIR)..."
	@if [ -w $(INSTALL_DIR) ]; then \
		cp $(OUTPUT) $(INSTALL_DIR)/$(BINARY_NAME); \
		chmod +x $(INSTALL_DIR)/$(BINARY_NAME); \
		echo "Installed successfully!"; \
	else \
		echo "Error: Cannot write to $(INSTALL_DIR). Try 'sudo make install'"; \
		exit 1; \
	fi

# アンインストール
.PHONY: uninstall
uninstall:
	@echo "Uninstalling $(BINARY_NAME)..."
	@if [ -f $(INSTALL_DIR)/$(BINARY_NAME) ]; then \
		rm -f $(INSTALL_DIR)/$(BINARY_NAME); \
		echo "Uninstalled successfully!"; \
	else \
		echo "$(BINARY_NAME) is not installed"; \
	fi

# クリーン
.PHONY: clean
clean:
	@echo "Cleaning build artifacts..."
	rm -rf $(BUILD_DIR)
	cd $(SRC_DIR) && $(GO) clean

# テスト実行
.PHONY: test
test:
	@echo "Running tests..."
	cd $(SRC_DIR) && $(GO) test -v ./...

# 依存関係の更新
.PHONY: deps
deps:
	@echo "Updating dependencies..."
	cd $(SRC_DIR) && $(GO) mod tidy
	cd $(SRC_DIR) && $(GO) mod download

# 開発用ビルド（デバッグ情報付き）
.PHONY: dev
dev: GOFLAGS += -gcflags="all=-N -l"
dev: build

# クロスコンパイル用ターゲット
.PHONY: build-all
build-all: build-linux build-darwin build-windows

.PHONY: build-linux
build-linux:
	@echo "Building for Linux..."
	cd $(SRC_DIR) && GOOS=linux GOARCH=amd64 $(GO) build $(GOFLAGS) -o ../$(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 ./cmd

.PHONY: build-darwin
build-darwin:
	@echo "Building for macOS..."
	cd $(SRC_DIR) && GOOS=darwin GOARCH=amd64 $(GO) build $(GOFLAGS) -o ../$(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 ./cmd
	cd $(SRC_DIR) && GOOS=darwin GOARCH=arm64 $(GO) build $(GOFLAGS) -o ../$(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 ./cmd

.PHONY: build-windows
build-windows:
	@echo "Building for Windows..."
	cd $(SRC_DIR) && GOOS=windows GOARCH=amd64 $(GO) build $(GOFLAGS) -o ../$(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe ./cmd


# ヘルプ
.PHONY: help
help:
	@echo "Task Organizer - Makefile targets:"
	@echo ""
	@echo "  make build      - Build the binary"
	@echo "  make install    - Build and install to $(INSTALL_DIR)"
	@echo "  make uninstall  - Remove installed binary"
	@echo "  make clean      - Clean build artifacts"
	@echo "  make test       - Run tests"
	@echo "  make deps       - Update dependencies"
	@echo "  make dev        - Build with debug information"
	@echo "  make build-all  - Build for all platforms"
	@echo "  make help       - Show this help message"
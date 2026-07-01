PLUGIN_NAME := model-mapper
DIST_DIR := dist
GO ?= go
WINDOWS_AMD64_OUT := $(DIST_DIR)/windows_amd64/$(PLUGIN_NAME).dll
LINUX_AMD64_OUT := $(DIST_DIR)/linux_amd64/$(PLUGIN_NAME).so
VERSION ?=
LINUX_AMD64_CC ?=
LINUX_AMD64_CC_BIN := $(firstword $(LINUX_AMD64_CC))

.PHONY: test build-windows-amd64 build-linux-amd64 build package install-local install-linux-amd64 smoke-local clean

test:
	$(GO) test ./...

build-windows-amd64:
	mkdir -p $(DIST_DIR)/windows_amd64
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 $(GO) build -buildmode=c-shared -o $(WINDOWS_AMD64_OUT) .

build-linux-amd64:
	@if [ -z "$(LINUX_AMD64_CC)" ]; then echo "LINUX_AMD64_CC is required for linux amd64 cgo cross-compile on Windows"; exit 1; fi
	@if ! command -v "$(LINUX_AMD64_CC_BIN)" >/dev/null 2>&1; then echo "Linux amd64 cross compiler not found: $(LINUX_AMD64_CC)"; exit 1; fi
	mkdir -p $(DIST_DIR)/linux_amd64
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 CC='$(LINUX_AMD64_CC)' $(GO) build -buildmode=c-shared -o $(LINUX_AMD64_OUT) .

build: build-windows-amd64 build-linux-amd64

package:
	$(GO) run .github/scripts/package-release.go -version "$(VERSION)" -dist $(DIST_DIR) -out $(DIST_DIR)/release

install-local: build-windows-amd64
	@if [ -z "$(CPA_PLUGINS_DIR)" ]; then echo "CPA_PLUGINS_DIR is required"; exit 1; fi
	mkdir -p "$(CPA_PLUGINS_DIR)"
	cp $(WINDOWS_AMD64_OUT) "$(CPA_PLUGINS_DIR)/$(PLUGIN_NAME).dll"

install-linux-amd64: build-linux-amd64
	@if [ -z "$(CPA_PLUGINS_DIR)" ]; then echo "CPA_PLUGINS_DIR is required"; exit 1; fi
	mkdir -p "$(CPA_PLUGINS_DIR)"
	cp $(LINUX_AMD64_OUT) "$(CPA_PLUGINS_DIR)/$(PLUGIN_NAME).so"

smoke-local: build-windows-amd64
	$(GO) run .github/scripts/smoke-local.go

clean:
	rm -rf $(DIST_DIR)

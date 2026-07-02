PLUGIN_NAME := model-mapper
DIST_DIR := dist
GO ?= go
GOOS ?=
GOARCH ?=
BUILD_CC ?=
VERSION ?=
LDFLAGS ?= -s -w
VERSION_LDFLAGS := $(if $(VERSION),-X main.pluginVersion=$(VERSION),)
WINDOWS_AMD64_OUT := $(DIST_DIR)/windows_amd64/$(PLUGIN_NAME).dll
LINUX_AMD64_OUT := $(DIST_DIR)/linux_amd64/$(PLUGIN_NAME).so
LINUX_AMD64_CC ?=
LINUX_AMD64_CC_BIN := $(firstword $(LINUX_AMD64_CC))

.PHONY: test vet build-platform build-windows-amd64 build-linux-amd64 build package-platform package install-local install-linux-amd64 smoke-local clean

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

build-platform:
	@if [ -z "$(GOOS)" ] || [ -z "$(GOARCH)" ]; then echo "GOOS and GOARCH are required"; exit 1; fi
	@case "$(GOOS)" in windows) ext=".dll" ;; darwin) ext=".dylib" ;; *) ext=".so" ;; esac; \
	out="$(DIST_DIR)/$(GOOS)_$(GOARCH)/$(PLUGIN_NAME)$$ext"; \
	mkdir -p "$$(dirname "$$out")"; \
	if [ -n "$(BUILD_CC)" ]; then export CC="$(BUILD_CC)"; fi; \
	CGO_ENABLED=1 GOOS="$(GOOS)" GOARCH="$(GOARCH)" $(GO) build -trimpath -buildmode=c-shared -ldflags='$(LDFLAGS) $(VERSION_LDFLAGS)' -o "$$out" .

build-windows-amd64:
	$(MAKE) --no-print-directory build-platform GOOS=windows GOARCH=amd64 GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)"

build-linux-amd64:
	@if [ -z "$(LINUX_AMD64_CC)" ]; then echo "LINUX_AMD64_CC is required for linux amd64 cgo cross-compile on Windows"; exit 1; fi
	@if ! command -v "$(LINUX_AMD64_CC_BIN)" >/dev/null 2>&1; then echo "Linux amd64 cross compiler not found: $(LINUX_AMD64_CC)"; exit 1; fi
	$(MAKE) --no-print-directory build-platform GOOS=linux GOARCH=amd64 GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)" BUILD_CC="$(LINUX_AMD64_CC)"

build: build-windows-amd64 build-linux-amd64

package-platform: build-platform
	@if [ -z "$(VERSION)" ]; then echo "VERSION is required"; exit 1; fi
	@case "$(GOOS)" in windows) ext=".dll" ;; darwin) ext=".dylib" ;; *) ext=".so" ;; esac; \
	library="$(DIST_DIR)/$(GOOS)_$(GOARCH)/$(PLUGIN_NAME)$$ext"; \
	archive="$(DIST_DIR)/$(PLUGIN_NAME)_$(VERSION)_$(GOOS)_$(GOARCH).zip"; \
	GOOS= GOARCH= CGO_ENABLED= $(GO) run .github/scripts/package-release.go -library "$$library" -archive "$$archive" -checksum "$$archive.sha256"

package:
	@if [ -n "$(GOOS)" ] || [ -n "$(GOARCH)" ]; then \
		$(MAKE) --no-print-directory package-platform VERSION="$(VERSION)" GOOS="$(GOOS)" GOARCH="$(GOARCH)" GO="$(GO)" DIST_DIR="$(DIST_DIR)" PLUGIN_NAME="$(PLUGIN_NAME)" BUILD_CC="$(BUILD_CC)" LDFLAGS="$(LDFLAGS)" VERSION_LDFLAGS="$(VERSION_LDFLAGS)"; \
	else \
		$(GO) run .github/scripts/package-release.go -version "$(VERSION)" -dist "$(DIST_DIR)" -out "$(DIST_DIR)/release"; \
	fi

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

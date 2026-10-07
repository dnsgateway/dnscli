REGISTRY ?= ghcr.io
USERNAME ?= sergelogvinov
OCIREPO ?= $(REGISTRY)/$(USERNAME)
PLATFORM ?= linux/arm64,linux/amd64
PUSH ?= false

SHA ?= $(shell git describe --match=none --always --abbrev=7 --dirty)
TAG ?= $(shell git describe --tag --always --match v[0-9]\*)
GO_LDFLAGS := -ldflags "-w -s -X main.version=$(TAG) -X main.commit=$(SHA)"

OS ?= $(shell go env GOOS)
ARCH ?= $(shell go env GOARCH)
ARCHS ?= amd64 arm64

BUILD_ARGS := --platform=$(PLATFORM)
ifeq ($(PUSH),true)
BUILD_ARGS += --push=$(PUSH)
else
BUILD_ARGS += --output type=docker
endif

COSING_ARGS ?=

############

# Help Menu

define HELP_MENU_HEADER
# Getting Started

To build this project, you must have the following installed:

- git
- make
- golang 1.27+
- golangci-lint

endef

export HELP_MENU_HEADER

help: ## This help menu
	@echo "$$HELP_MENU_HEADER"
	@grep -E '^[a-zA-Z0-9%_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

############
#
# Build Abstractions
#

.PHONY: clean
clean: ## Clean
	rm -rf bin/ dist/ .cache/ .gocache/

.PHONY: tools
tools:
	go install github.com/google/go-licenses@latest

.PHONY: build ## Build
build:
	CGO_ENABLED=1 GOOS=$(OS) GOARCH=$(ARCH) go build $(GO_LDFLAGS) \
		-o bin/dnscli-$(ARCH) ./cmd/dnscli

.PHONY: build-dev
build-dev: ## Build for local Touch ID testing (unsigned, NOT for release)
	CGO_ENABLED=1 GOOS=$(OS) GOARCH=$(ARCH) go build $(GO_LDFLAGS) -tags devkeychain \
		-o bin/dnscli ./cmd/dnscli

.PHONY: run
run: ## Run
	go run $(GO_LDFLAGS) ./cmd/dnscli

.PHONY: lint
lint: ## Lint Code
	golangci-lint run --config .golangci.yml

.PHONY: vet
vet: ## Vet Code
	go vet ./...

.PHONY: unit
unit: ## Unit Tests
	go test $(shell go list ./...) $(TESTARGS)

.PHONY: test
test: lint unit ## Run all tests

.PHONY: install
install: build
	cp ./bin/dnscli-$(ARCH) ~/go/bin/dnscli

.PHONY: licenses
licenses:
	go-licenses check ./... --disallowed_types=forbidden,restricted,unknown

.PHONY: conformance
conformance: ## Conformance
	docker run --rm -it -v $(PWD):/src -w /src ghcr.io/siderolabs/conform:v0.1.0-alpha.31 enforce

############
#
# macOS release signing
#
# An unsigned binary may not use the data protection keychain, so the
# Keychain Unseal Method is refused (build-dev bypasses that for local
# testing only). A release puts dnscli in a minimal .app bundle that embeds
# a provisioning profile granting keychain-access-groups, signs it, and
# notarizes it. Users run it through a symlink:
#   ln -s /Applications/dnscli.app/Contents/MacOS/dnscli /usr/local/bin/dnscli
#
# One-time setup in the Apple Developer portal (developer.apple.com):
#   1. Register the App ID $(MACOS_BUNDLE_ID) (explicit, no capabilities needed).
#   2. Create a "Developer ID Application" certificate and install it in the
#      login keychain; `security find-identity -v -p codesigning` shows it.
#   3. Create a "Developer ID" provisioning profile for that App ID and
#      certificate, download it, and pass it as MACOS_PROFILE.
#   4. Store notarization credentials once:
#        xcrun notarytool store-credentials $(MACOS_NOTARY_PROFILE) \
#          --apple-id <you@example.com> --team-id <TEAMID> --password <app-specific-password>
#
# Then, on a Mac:
#   make macos-release MACOS_TEAM_ID=ABCDE12345 \
#     MACOS_IDENTITY="Developer ID Application: Name (ABCDE12345)" \
#     MACOS_PROFILE=~/Downloads/dnscli.provisionprofile
#
# For local testing with a free Apple ID instead: use an "Apple Development"
# identity and a development profile from Xcode's Personal Team, then run
# macos-sign (and skip macos-notarize).

MACOS_BUNDLE_ID ?= com.github.sergelogvinov.dnscli
MACOS_TEAM_ID ?=
MACOS_IDENTITY ?=
MACOS_PROFILE ?=
MACOS_NOTARY_PROFILE ?= dnscli-notary
MACOS_APP := bin/dnscli.app

.PHONY: macos-bundle
macos-bundle: build ## Put the darwin binary in bin/dnscli.app
	@test "$(OS)" = darwin || { echo "macos-bundle runs on macOS (OS=$(OS))"; exit 1; }
	@test -n "$(MACOS_PROFILE)" || { echo "set MACOS_PROFILE to the .provisionprofile"; exit 1; }
	rm -rf $(MACOS_APP)
	mkdir -p $(MACOS_APP)/Contents/MacOS
	cp bin/dnscli-$(ARCH) $(MACOS_APP)/Contents/MacOS/dnscli
	cp "$(MACOS_PROFILE)" $(MACOS_APP)/Contents/embedded.provisionprofile
	sed -e 's/@BUNDLE_ID@/$(MACOS_BUNDLE_ID)/g' -e 's/@VERSION@/$(patsubst v%,%,$(TAG))/g' \
		hack/macos/Info.plist.in > $(MACOS_APP)/Contents/Info.plist

.PHONY: macos-sign
macos-sign: macos-bundle ## Sign bin/dnscli.app with the keychain entitlement
	@test -n "$(MACOS_TEAM_ID)" || { echo "set MACOS_TEAM_ID"; exit 1; }
	@test -n "$(MACOS_IDENTITY)" || { echo "set MACOS_IDENTITY (security find-identity -v -p codesigning)"; exit 1; }
	sed -e 's/@BUNDLE_ID@/$(MACOS_BUNDLE_ID)/g' -e 's/@TEAM_ID@/$(MACOS_TEAM_ID)/g' \
		hack/macos/dnscli.entitlements.in > bin/dnscli.entitlements
	codesign --force --timestamp --options runtime \
		--entitlements bin/dnscli.entitlements \
		--sign "$(MACOS_IDENTITY)" $(MACOS_APP)
	codesign --verify --strict --verbose=2 $(MACOS_APP)
	codesign --display --entitlements - $(MACOS_APP)

.PHONY: macos-notarize
macos-notarize: macos-sign ## Notarize and staple bin/dnscli.app (Developer ID only)
	ditto -c -k --keepParent $(MACOS_APP) bin/dnscli-macos.zip
	xcrun notarytool submit bin/dnscli-macos.zip --keychain-profile $(MACOS_NOTARY_PROFILE) --wait
	xcrun stapler staple $(MACOS_APP)
	ditto -c -k --keepParent $(MACOS_APP) bin/dnscli-macos-$(ARCH).zip

.PHONY: macos-release
macos-release: macos-notarize ## Bundle, sign and notarize the macOS release

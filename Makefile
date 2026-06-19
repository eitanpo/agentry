# Local build/install. The base version is canonical in main.go (var Version); here we append
# a UTC build timestamp as the semver build-metadata segment so every local build is distinct.
# Plain `go build`/`go install` (no make) print the bare base version.
BASE    := $(shell sed -n 's/^var Version = "\(.*\)"/\1/p' main.go)
VERSION := $(BASE)+$(shell date -u +%Y%m%dT%H%M%SZ)
LDFLAGS := -ldflags "-X main.Version=$(VERSION)"

.PHONY: build install release release-dry
build:
	go build $(LDFLAGS) -o agentry .
install:
	go install $(LDFLAGS) .

# Publish a release from the current pushed tag, then refresh this machine's
# install so it runs what was just shipped — the step that's otherwise forgotten.
# Assumes the version tag is already created and pushed (see DEVELOPMENT.md).
release:
	GITHUB_TOKEN=$$(gh auth token) HOMEBREW_TAP_GITHUB_TOKEN=$$(gh auth token) goreleaser release --clean
	$(MAKE) install

# Dry run: build all targets locally, publish nothing, install nothing.
release-dry:
	goreleaser release --snapshot --clean

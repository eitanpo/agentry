# Local build/install. The base version is canonical in main.go (var Version); here we append
# a UTC build timestamp as the semver build-metadata segment so every local build is distinct.
# Plain `go build`/`go install` (no make) print the bare base version.
BASE    := $(shell sed -n 's/^var Version = "\(.*\)"/\1/p' main.go)
VERSION := $(BASE)+$(shell date -u +%Y%m%dT%H%M%SZ)
LDFLAGS := -ldflags "-X main.Version=$(VERSION)"

.PHONY: build install
build:
	go build $(LDFLAGS) -o agentry .
install:
	go install $(LDFLAGS) .

# Local build/install. The base version is canonical in main.go (var Version); here we append
# a UTC build timestamp as the semver build-metadata segment so every local build is distinct,
# plus a ".dirty" marker when the working tree has uncommitted changes — so the global binary's
# --version shows at a glance it is an unreleased dev build, not the clean release.
# Plain `go build`/`go install` (no make) print the bare base version.
BASE    := $(shell sed -n 's/^var Version = "\(.*\)"/\1/p' main.go)
DIRTY   := $(shell test -n "$$(git status --porcelain 2>/dev/null)" && echo .dirty)
VERSION := $(BASE)+$(shell date -u +%Y%m%dT%H%M%SZ)$(DIRTY)
LDFLAGS := -ldflags "-X main.Version=$(VERSION)"

# build is the default goal: it compile-checks the whole module, then installs to ~/go/bin so
# the global `agentry` (which resolves sessions from the cwd, not this repo) always reflects the
# latest work. Running the build IS the install — there is no non-installing variant. `install`
# is the install step on its own, reused by `release`.
.DEFAULT_GOAL := build

.PHONY: build fmt install release release-dry schema-scan schema-scan-new schema-scan-test
build: fmt
	go build ./...
	go install $(LDFLAGS) .
install:
	go install $(LDFLAGS) .

# Fail the build on an unformatted file, rather than leaving the drift for whoever
# next runs gofmt by hand: two files had been unformatted for long enough that
# nobody knew which change introduced it. Wired into build (25ms over this module)
# because a check nothing triggers is a check nothing catches. `gofmt -l` lists the
# offenders and still exits 0, so the recipe tests the list rather than the status.
# Blocked by a slip you do not want to fix now? `make install` skips straight to it.
fmt:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt: not formatted — run 'gofmt -w' on:"; \
		echo "$$unformatted" | sed 's/^/  /'; \
		exit 1; \
	fi

# Measure how far docs/session-format.md has drifted from the logs on this machine.
# Deliberately not wired into build: it sweeps ~500MB and takes minutes, so it is a
# thing you run when touching the parser or the format doc, not on every compile.
# `make schema-scan-new` narrows the report to elements the doc has never named.
schema-scan:
	scripts/schema-scan.sh
schema-scan-new:
	scripts/schema-scan.sh --new
schema-scan-test:
	scripts/schema-scan_test.sh

# Publish a release from the current pushed tag, then refresh this machine's
# install so it runs what was just shipped — the step that's otherwise forgotten.
# Assumes the version tag is already created and pushed (see DEVELOPMENT.md).
release:
	GITHUB_TOKEN=$$(gh auth token) HOMEBREW_TAP_GITHUB_TOKEN=$$(gh auth token) goreleaser release --clean
	$(MAKE) install

# Dry run: build all targets locally, publish nothing, install nothing.
release-dry:
	goreleaser release --snapshot --clean

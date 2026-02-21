VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT)

.PHONY: build clean test lint install symlink-skill build-all build-linux build-darwin release snapshot

build: symlink-skill
	go build -ldflags "$(LDFLAGS)" -o bin/crabctl .

clean:
	rm -rf bin/

test:
	go test ./...

lint:
	golangci-lint run

install: build
	cp bin/crabctl $(GOPATH)/bin/

symlink-skill:
	@mkdir -p ~/.claude/skills/crab
	@ln -sf $(CURDIR)/.claude/skills/crab/SKILL.md ~/.claude/skills/crab/SKILL.md
	@echo "Symlinked ~/.claude/skills/crab/SKILL.md -> $(CURDIR)/.claude/skills/crab/SKILL.md"

build-all: build-linux build-darwin

build-linux:
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/crabctl-linux-amd64 .

build-darwin:
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/crabctl-darwin-arm64 .

release:
	goreleaser release --clean

snapshot:
	goreleaser release --snapshot --clean

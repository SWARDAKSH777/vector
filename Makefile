BINARY      ?= vector
FRONTEND    := ./frontend
BACKEND     := ./backend
VERSION     ?= dev
COMMIT      ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || echo unknown)
BUILD_TIME  ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
RELEASE_GO  ?= go1.26.4
GOFLAGS     := -trimpath -buildvcs=false
LDFLAGS     := -s -w -X main.buildVersion=$(VERSION) -X main.buildCommit=$(COMMIT) -X main.buildTime=$(BUILD_TIME)

.PHONY: all frontend backend backend-only check-release-go release-linux release-linux-embedded test race vet audit validate-source dev-backend dev-frontend clean

all: frontend backend

frontend:
	cd $(FRONTEND) && npm ci --include=optional && npm run typecheck && npm run build
	rm -rf $(BACKEND)/web
	cp -a $(FRONTEND)/dist $(BACKEND)/web

backend:
	cd $(BACKEND) && CGO_ENABLED=1 go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o ../$(BINARY) .

backend-only:
	cd $(BACKEND) && CGO_ENABLED=1 go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o ../$(BINARY) .

check-release-go:
	@actual="$$(cd $(BACKEND) && GOTOOLCHAIN=local go env GOVERSION)"; \
	if [ "$$actual" != "$(RELEASE_GO)" ]; then \
		echo "ERROR: official releases must be built with $(RELEASE_GO); found $$actual" >&2; \
		echo "Use the pinned toolchain from backend/go.mod or set RELEASE_GO only after a documented security review." >&2; \
		exit 1; \
	fi

release-linux: check-release-go frontend
	cd $(BACKEND) && CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o ../$(BINARY)-linux-amd64 .

# Build from the already audited/embedded frontend assets. This is used by the
# ground-zero deployment bundle so the VPS does not need Node/npm at install time.
release-linux-embedded: check-release-go
	@test -f $(BACKEND)/web/index.html || (echo "ERROR: embedded frontend assets are missing; run make frontend first" >&2; exit 1)
	cd $(BACKEND) && CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o ../$(BINARY)-linux-amd64 .

test:
	cd $(BACKEND) && GOTOOLCHAIN=local go test ./...

race:
	cd $(BACKEND) && GOTOOLCHAIN=local go test -race -timeout=300s -count=1 .
	cd $(BACKEND) && GOTOOLCHAIN=local go test -race -timeout=120s -count=1 ./sqlite3local ./qrcode/...

vet:
	cd $(BACKEND) && GOTOOLCHAIN=local go vet ./...

audit:
	cd $(FRONTEND) && npm audit --audit-level=high
	cd $(BACKEND) && GOTOOLCHAIN=local go test ./...

validate-source:
	scripts/validate-source.sh

dev-backend:
	mkdir -p ./data
	cd $(BACKEND) && DATA_DIR=../data LISTEN_ADDR=127.0.0.1:8081 INTERNAL_PORT=8081 go run .

dev-frontend:
	cd $(FRONTEND) && npm run dev

clean:
	rm -f $(BINARY) $(BINARY)-linux-amd64
	rm -rf $(BACKEND)/web $(FRONTEND)/dist

.PHONY: all build test check dev clean verify-backup eval trial

GO ?= go
GOFMT ?= gofmt
CARGO ?= cargo
NPM ?= npm

all: build

build:
	cd frontend && $(NPM) run build
	cd backend && $(GO) build -o ../bin/listenledger .
	@if command -v $(CARGO) >/dev/null 2>&1; then \
		echo "Building edge crate..."; \
		cd edge && $(CARGO) build; \
	fi

test:
	cd backend && $(GO) test -race ./...
	cd frontend && $(NPM) test
	@if command -v $(CARGO) >/dev/null 2>&1; then \
		echo "Testing edge crate..."; \
		cd edge && $(CARGO) test; \
	fi

check:
	@echo "Checking Go formatting..."
	@test -z "$$(cd backend && $(GOFMT) -l .)" || (echo "Unformatted Go files detected" && exit 1)
	@echo "Running Go vet..."
	cd backend && $(GO) vet ./...
	@echo "Running frontend typecheck..."
	cd frontend && $(NPM) run typecheck
	@if command -v $(CARGO) >/dev/null 2>&1; then \
		echo "Checking edge crate..."; \
		cd edge && $(CARGO) check; \
	fi

verify-backup:
	./scripts/verify_backup_restore.sh
	./scripts/verify_postgres_backup.sh

eval:
	cd backend && $(GO) test -v -race ./eval

trial: eval

dev:
	cd backend && $(GO) run . -addr 127.0.0.1:8080 -dev

clean:
	rm -rf bin/ frontend/dist/ edge/target/ *.db *.db-wal *.db-shm

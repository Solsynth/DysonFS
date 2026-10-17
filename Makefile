BINARY := dysonfs
SWAG := swag
CONFIG ?= config.toml

# The module targets Go 1.26.2; pin it so a newer host toolchain does not pick
# up dependency breakage the project has not migrated to yet.
GOTOOLCHAIN ?= go1.26.2
export GOTOOLCHAIN

.PHONY: build run start dev web web-dev web-check web-test test lint tidy swagger migrate-legacy

build:
	go build -o bin/$(BINARY) ./cmd

# Build the SPA into web/dist.
web:
	cd web && bun install --frozen-lockfile && bun run build

web-check:
	$(MAKE) -C web check

web-dev:
	cd web && bun run dev

web-test:
	cd web && bun run test:unit --run

run:
	CONFIG_PATH=$(CONFIG) go run ./cmd --mode master

# Build the console and the service, then start the master on :8080.
# The master serves web/dist at / (see web.staticDir).
start: web build
	CONFIG_PATH=$(CONFIG) ./bin/$(BINARY) --mode master

# Two processes for development: Go API on :8080, Vite with /api proxied.
dev:
	@CONFIG_PATH=$(CONFIG) go run ./cmd --mode master & \
	server=$$!; trap 'kill $$server 2>/dev/null' INT TERM EXIT; \
	$(MAKE) -C web dev

# Schema changes are applied by the master on startup. This imports the legacy
# C# database; set LEGACY_DATABASE_DSN before running it.
migrate-legacy:
	CONFIG_PATH=$(CONFIG) go run ./cmd migrate-legacy

test:
	go test ./...

lint:
	go vet ./...

tidy:
	go mod tidy

swagger:
	$(SWAG) init -g cmd/main.go -o docs --parseDependency --parseInternal

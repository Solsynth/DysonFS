# Dyson Drive web console — one-word entry points.
#
#   make install   install dependencies
#   make dev       Vite dev server (proxies /api to :8080)
#   make serve     build the SPA, then run the Go master
#   make check     type-check, lint, tests, production build
#
# CONFIG points at a DysonFS TOML read by the Go server from the repo root.

BUN ?= bun
CONFIG ?= config.toml

.PHONY: install dev build preview test type-check lint format check serve

install:
	$(BUN) install

dev:
	$(BUN) run dev

build:
	$(BUN) run build

preview:
	$(BUN) run preview

test:
	$(BUN) run test:unit --run

type-check:
	$(BUN) run type-check

lint:
	$(BUN) run lint

format:
	$(BUN) run format

check: type-check lint test build

# Build the SPA, then run the Go master, which serves dist/ at /.
serve: build
	cd .. && $(MAKE) CONFIG=$(CONFIG) run

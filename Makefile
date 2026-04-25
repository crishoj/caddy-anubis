ANUBIS_DIR ?= ../anubis
ANUBIS_REPO ?= https://github.com/TecharoHQ/anubis

.PHONY: setup assets run test help

help:
	@echo "Targets:"
	@echo "  setup  - Clone Anubis as a sibling, build its assets, write go.work"
	@echo "  assets - Rebuild Anubis embedded assets (after upstream changes)"
	@echo "  run    - Start Caddy with the example Caddyfile on :8080"
	@echo "  test   - Run all tests with -race"

# One-time setup: clone Anubis next to this repo, build its embedded
# assets, and write a developer-local go.work pointing the workspace at
# the clone. Idempotent.
#
# Requirements: Go 1.25+, npm, Bash 4+ on PATH (macOS: `brew install bash`).
setup: $(ANUBIS_DIR)/.git assets go.work
	@echo "Setup complete. Run 'make run' to start Caddy."

$(ANUBIS_DIR)/.git:
	git clone $(ANUBIS_REPO) $(ANUBIS_DIR)

assets:
	# cd into the anubis dir rather than `make -C`: their Makefile uses
	# $(PWD) (which is the shell env var, untouched by -C) instead of
	# $(CURDIR) when computing node_modules/.bin's location, so -C points
	# at the wrong PATH and breaks the postcss/esbuild lookup.
	cd $(ANUBIS_DIR) && $(MAKE) assets

go.work:
	@printf 'go 1.25\n\nuse .\n\nreplace github.com/TecharoHQ/anubis => %s\n' "$$(cd $(ANUBIS_DIR) && pwd)" > go.work
	@echo "Wrote go.work pointing at $(ANUBIS_DIR)"

run:
	go run ./cmd/caddy run --config Caddyfile

test:
	go test -race ./...

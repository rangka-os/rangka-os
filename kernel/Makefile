# Rangka OS kernel. Needs Go 1.24+ and, for DB tests and the demo, PostgreSQL 16+ server binaries
# (initdb/pg_ctl/postgres; scripts/pgtemp.sh finds Debian, Homebrew, DBngin and Postgres.app installs, else set PGBIN).

GO      ?= go
# Workspace that holds rangka-connectors/: $RANGKA_WORKSPACE, else the first ancestor of this repo's parent with
# rangka-apps/apps (works from a git worktree), else two levels up.
WS      ?= $(shell scripts/workspace.sh)
# rangka-apps as of v0 (0.4 layout), so the kernel does not follow rangka-apps' layout (contract 0001)
CAT     := testdata/catalogue-v0
APPS    ?= $(CAT)/apps
SITE    ?= $(CAT)/sites/kopi-senja
CONN    ?= $(WS)/rangka-connectors
ADDR    ?= :8080
BIN     := bin/rangka
# rangka-ai checkout whose dist/cli.js run-demo starts as the Raka sidecar (skipped when not built)
RANGKA_AI_DIR ?= $(WS)/rangka-ai
AI_PORT ?= 8787
MODULE  ?=

.PHONY: build test test-unit vet validate compare-python run-demo sync-schemas proto-expected rename-module clean

build:
	$(GO) build -o $(BIN) ./cmd/rangka

# All tests, against a throwaway Postgres cluster started for the run and removed afterwards.
test:
	@URL=$$(PGTEMP_STATE=.pgtemp-test scripts/pgtemp.sh start) || exit 1; \
	echo "temporary postgres: $$URL"; \
	RANGKA_TEST_DATABASE_URL="$$URL" $(GO) test -count=1 ./...; rc=$$?; \
	PGTEMP_STATE=.pgtemp-test scripts/pgtemp.sh stop; exit $$rc

# Tests that need no database (DB tests skip).
test-unit:
	$(GO) test ./...

vet:
	$(GO) vet ./...
	@test -z "$$(gofmt -l .)" || (gofmt -l .; echo "run gofmt -w ."; exit 1)

validate: build
	$(BIN) validate --apps $(APPS) --site $(SITE) --connectors $(CONN)

# The Go validator and rangka_validate.py must print the same reports: the real apps + site + connectors, the overlay and numbering sites,
# the broken apps copy, the broken apps + broken site (overlays, theme contrast) + broken connectors, and the
# contract 0001 layout (modules/ + apps/), valid and broken, print formats + letterheads, valid and broken, and
# processes (contract 0014: the canvas snapshot and a broken set), declared stock moves (contract 0021), valid and broken, work screens (contract 0030), valid and broken, the catalogue keys (contract 0036), valid and broken, and config.modules (contract 0032), valid and broken.
# template_error (a template Go's html/template cannot parse) is kernel only: Python has no Go template parser,
# so the broken print input has no such template.
compare-python: build
	@D=$$(mktemp -d); rc=0; \
	for args in "$(APPS) --site $(SITE) --connectors $(CONN)" "$(APPS) --site testdata/overlay-site --connectors $(CONN)" \
	            "$(APPS) --site testdata/numbering-site --connectors $(CONN)" "testdata/broken-apps" \
	            "testdata/broken-apps --site testdata/broken-site --connectors testdata/broken-connectors" \
	            "testdata/modules-apps/apps --site testdata/modules-apps/site" \
	            "testdata/broken-modules-apps/apps --site testdata/broken-modules-apps/site" \
	            "testdata/nav-apps/apps --site testdata/nav-apps/site" \
	            "testdata/broken-nav-apps/apps --site testdata/broken-nav-apps/site" \
	            "testdata/print-apps/apps --site testdata/print-site" \
	            "testdata/broken-print-apps/apps --site testdata/broken-print-site" \
	            "testdata/canvas/apps --site testdata/canvas/site" \
	            "testdata/broken-canvas/apps --site testdata/broken-canvas/site" \
	            "testdata/modules-apps/apps --site testdata/site-module" \
	            "testdata/modules-apps/apps --site testdata/broken-site-module" \
	            "testdata/stock-apps/apps --site testdata/stock-apps/site" \
	            "testdata/broken-stock-apps/apps --site testdata/broken-stock-apps/site" \
	            "testdata/screens/apps --site testdata/screens/site" \
	            "testdata/broken-screens/apps --site testdata/broken-screens/site" \
	            "testdata/catalogue-keys/apps --site testdata/catalogue-keys/site --connectors testdata/catalogue-keys/connectors" \
	            "testdata/broken-catalogue-keys/apps --site testdata/broken-catalogue-keys/site --connectors testdata/broken-catalogue-keys/connectors" \
	            "testdata/config-modules/apps --site testdata/config-modules/site" \
	            "testdata/broken-config-modules/apps --site testdata/broken-config-modules/site"; do \
	  python3 ../tools/rangka_validate.py $$args > $$D/py.txt; \
	  set -- $$args; a=$$1; shift; $(BIN) validate --apps $$a "$$@" > $$D/go.txt; \
	  if diff $$D/py.txt $$D/go.txt; then echo "identical ($$(tail -1 $$D/go.txt)): $$args"; else rc=1; fi; \
	done; rm -rf $$D; exit $$rc

# Temp Postgres → migrate → seed (org kopi-senja, ceo@saga.co.id / rangka-demo) → serve on :8080 with
# --dev-open. When rangka-ai is built ($(RANGKA_AI_DIR)/dist/cli.js; set RANGKA_AI_DIR when the workspace has no
# rangka-ai/), its scripted sidecar (serve --demo, no LLM key) runs on 127.0.0.1:$(AI_PORT) with a random shared
# secret and the kernel gets --ai-url, so Raka answers in /app/ and /console/ (contract 0020).
# Ctrl-C stops the server and the sidecar and removes the database.
run-demo: build
	@URL=$$(PGTEMP_STATE=.pgtemp-demo scripts/pgtemp.sh start) || exit 1; \
	AI_PID=; trap 'test -n "$$AI_PID" && kill $$AI_PID 2>/dev/null; PGTEMP_STATE=.pgtemp-demo scripts/pgtemp.sh stop' EXIT INT TERM; \
	echo "temporary postgres: $$URL"; \
	$(BIN) migrate --apps $(APPS) --site $(SITE) --connectors $(CONN) --db "$$URL" | tail -1 && \
	$(BIN) seed --apps $(APPS) --site $(SITE) --connectors $(CONN) --db "$$URL" || exit 1; \
	AI_FLAGS=; \
	if [ -f "$(RANGKA_AI_DIR)/dist/cli.js" ] && command -v node >/dev/null; then \
	  RANGKA_AI_SECRET=$$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n'); export RANGKA_AI_SECRET; \
	  RAKA_SERVE_TOKEN=$$RANGKA_AI_SECRET node "$(RANGKA_AI_DIR)/dist/cli.js" serve --demo --port $(AI_PORT) & AI_PID=$$!; \
	  AI_FLAGS="--ai-url http://127.0.0.1:$(AI_PORT)"; \
	  i=0; until curl -fs -o /dev/null http://127.0.0.1:$(AI_PORT)/healthz; do \
	    i=$$((i+1)); if [ $$i -ge 25 ]; then echo "peringatan: Raka belum menjawab di http://127.0.0.1:$(AI_PORT)/healthz setelah 5 detik"; break; fi; sleep 0.2; \
	  done; \
	elif [ ! -f "$(RANGKA_AI_DIR)/dist/cli.js" ]; then \
	  echo "Raka tidak dijalankan: $(RANGKA_AI_DIR)/dist/cli.js belum ada (npm run build)"; \
	fi; \
	echo "open http://localhost$(ADDR)/app/  ·  API http://localhost$(ADDR)/api/v1/meta  ·  MCP POST http://localhost$(ADDR)/mcp" && \
	$(BIN) serve --apps $(APPS) --site $(SITE) --connectors $(CONN) --db "$$URL" --addr $(ADDR) --dev-open $$AI_FLAGS

# Copy rangka-os/schema/0.5 (and 0.4 app.json, for legacy app.rk files) into the binary's embedded copy
# (a test fails when they differ).
sync-schemas:
	cp ../schema/0.5/*.json internal/schema/0.5/ && cp ../schema/0.4/app.json internal/schema/0.4/ && cp ../web/design-system/tokens.json internal/theme/tokens.json && \
	rm -rf internal/skills/platform && mkdir -p internal/skills/platform && cp -R ../skills/. internal/skills/platform/

# Recompute the prototype's dashboard numbers used by the dashboard tests (needs node).
proto-expected:
	scripts/proto-expected.sh

# Rename the Go module (the GitHub org is a placeholder): make rename-module MODULE=github.com/acme/rangka-os/kernel
rename-module:
	@test -n "$(MODULE)" || (echo "usage: make rename-module MODULE=github.com/<org>/rangka-os/kernel"; exit 1)
	@OLD=$$($(GO) list -m); $(GO) mod edit -module $(MODULE); \
	grep -rl "$$OLD" --include='*.go' . | xargs sed -i "s#$$OLD#$(MODULE)#g"; echo "renamed $$OLD → $(MODULE)"

clean:
	rm -rf bin

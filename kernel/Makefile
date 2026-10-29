# Rangka OS kernel. Needs Go 1.24+ and, for DB tests and the demo, PostgreSQL 16 binaries
# (initdb/pg_ctl; set PGBIN if they are not in /usr/lib/postgresql/<v>/bin).

GO      ?= go
WS      := $(abspath ../..)
APPS    ?= $(WS)/rangka-apps/apps
SITE    ?= $(WS)/rangka-apps/sites/kopi-senja
CONN    ?= $(WS)/rangka-connectors
ADDR    ?= :8080
BIN     := bin/rangka
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

# The Go validator and rangka_validate.py must print the same reports: the real apps + site + connectors,
# the broken apps copy, and the broken apps + broken site (overlays, theme contrast) + broken connectors.
compare-python: build
	@D=$$(mktemp -d); rc=0; \
	for args in "$(APPS) --site $(SITE) --connectors $(CONN)" "$(APPS) --site testdata/overlay-site --connectors $(CONN)" "testdata/broken-apps" \
	            "testdata/broken-apps --site testdata/broken-site --connectors testdata/broken-connectors"; do \
	  python3 ../tools/rangka_validate.py $$args > $$D/py.txt; \
	  set -- $$args; a=$$1; shift; $(BIN) validate --apps $$a "$$@" > $$D/go.txt; \
	  if diff $$D/py.txt $$D/go.txt; then echo "identical ($$(tail -1 $$D/go.txt)): $$args"; else rc=1; fi; \
	done; rm -rf $$D; exit $$rc

# Temp Postgres → migrate → seed (org kopi-senja, ceo@saga.co.id / rangka-demo) → serve on :8080 with
# --dev-open. Ctrl-C stops the server and removes the database.
run-demo: build
	@URL=$$(PGTEMP_STATE=.pgtemp-demo scripts/pgtemp.sh start) || exit 1; \
	trap 'PGTEMP_STATE=.pgtemp-demo scripts/pgtemp.sh stop' EXIT INT TERM; \
	echo "temporary postgres: $$URL"; \
	$(BIN) migrate --apps $(APPS) --site $(SITE) --db "$$URL" | tail -1 && \
	$(BIN) seed --apps $(APPS) --site $(SITE) --db "$$URL" && \
	echo "open http://localhost$(ADDR)/prototype/  ·  API http://localhost$(ADDR)/api/v1/meta  ·  MCP POST http://localhost$(ADDR)/mcp" && \
	$(BIN) serve --apps $(APPS) --site $(SITE) --db "$$URL" --addr $(ADDR) --dev-open

# Copy rangka-os/schema/0.4 into the binary's embedded copy (a test fails when they differ).
sync-schemas:
	cp ../schema/0.4/*.json internal/schema/0.4/ && cp ../web/design-system/tokens.json internal/theme/tokens.json

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

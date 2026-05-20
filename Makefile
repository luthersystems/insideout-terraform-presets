# Repo top-level Makefile. Targets are intentionally narrow — most of the
# day-to-day work uses `go test`, `terraform fmt`, and the .claude skills.
# This file exists primarily for the imported-resource codegen flow:
#
#   make refresh-schemas   re-dump provider schemas (developer-only;
#                          requires terraform on PATH)
#   make gen-imported      regenerate Layer 1 typed structs from the
#                          committed schemas/*.filtered.json
#   make verify-gen        what CI runs — gen-imported + git diff gate
#
# Add new targets here only when they land into CI or have widespread
# developer adoption.

GO ?= go
SCHEMAS_DIR := schemas
GEN_DIR := pkg/composer/imported/generated

.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

.PHONY: refresh-schemas
refresh-schemas: ## Re-dump terraform provider schemas (requires `terraform` on PATH).
	@command -v terraform >/dev/null || { echo "terraform not found on PATH"; exit 1; }
	cd $(SCHEMAS_DIR) && terraform init -upgrade -input=false
	cd $(SCHEMAS_DIR) && terraform providers schema -json > .full.json
	$(GO) run ./cmd/imported-codegen filter \
	    --in $(SCHEMAS_DIR)/.full.json \
	    --aws-out $(SCHEMAS_DIR)/aws.filtered.json \
	    --google-out $(SCHEMAS_DIR)/google.filtered.json \
	    --google-beta-out $(SCHEMAS_DIR)/google-beta.filtered.json
	rm $(SCHEMAS_DIR)/.full.json

.PHONY: gen-imported
gen-imported: ## Regenerate typed resource files from filtered schemas.
	$(GO) run ./cmd/imported-codegen gen \
	    --aws-schema $(SCHEMAS_DIR)/aws.filtered.json \
	    --google-schema $(SCHEMAS_DIR)/google.filtered.json \
	    --google-beta-schema $(SCHEMAS_DIR)/google-beta.filtered.json \
	    --providers-tf $(SCHEMAS_DIR)/providers.tf \
	    --out $(GEN_DIR)
	$(GO) build ./...

# Output dir for the TS Zod emitter. Defaults to .tmp/zod-out (gitignored).
# Downstream consumers point ZOD_OUT at their TS source tree, e.g.:
#   make gen-zod ZOD_OUT=$$HOME/work/reliable/lib/stack/imported
ZOD_OUT ?= .tmp/zod-out

.PHONY: gen-zod
gen-zod: ## Emit TS Zod fragments to ZOD_OUT (default .tmp/zod-out).
	$(GO) run ./cmd/imported-codegen zod \
	    --aws-schema $(SCHEMAS_DIR)/aws.filtered.json \
	    --google-schema $(SCHEMAS_DIR)/google.filtered.json \
	    --google-beta-schema $(SCHEMAS_DIR)/google-beta.filtered.json \
	    --providers-tf $(SCHEMAS_DIR)/providers.tf \
	    --out $(ZOD_OUT)

.PHONY: verify-gen
verify-gen: gen-imported ## Fail if regenerating produces a diff (CI gate).
	@if ! git diff --exit-code -- $(GEN_DIR) $(SCHEMAS_DIR); then \
	    echo ""; \
	    echo "==> Generated output is stale. Run 'make gen-imported' and commit."; \
	    exit 1; \
	fi

SUPPORTED_RESOURCES_MD := SUPPORTED_RESOURCES.md

.PHONY: regen-supported-resources
regen-supported-resources: ## Regenerate SUPPORTED_RESOURCES.md from the capabilities matrix.
	$(GO) run ./cmd/imported-codegen supported-resources --output $(SUPPORTED_RESOURCES_MD)

.PHONY: verify-supported-resources
verify-supported-resources: ## Fail if SUPPORTED_RESOURCES.md is stale (CI gate, #492).
	$(GO) run ./cmd/imported-codegen supported-resources --check --output $(SUPPORTED_RESOURCES_MD)

.PHONY: test
test: ## Run go test -race for the whole module.
	$(GO) test -race ./...

.PHONY: go-fmt-check
go-fmt-check: ## Fail if any tracked Go file is not gofmt-clean (CI gate, #647).
	@unformatted=$$(gofmt -l $$(git ls-files '*.go')); \
	  if [ -n "$$unformatted" ]; then \
	    echo "==> These Go files are not gofmt-clean. Run 'gofmt -w' on them:"; \
	    echo "$$unformatted"; \
	    exit 1; \
	  fi

.PHONY: verify-phantom-schema
verify-phantom-schema: ## Validate phantom-computed-fields.txt against pinned provider schema.
	@command -v terraform >/dev/null || { echo "terraform not found on PATH"; exit 1; }
	@command -v jq >/dev/null || { echo "jq not found on PATH"; exit 1; }
	@tmp=$$(mktemp -d); \
	  cp $(SCHEMAS_DIR)/providers.tf "$$tmp/"; \
	  ( cd "$$tmp" && terraform init -input=false -upgrade >/dev/null && \
	    terraform providers schema -json > schema.json ) && \
	  bash tests/verify-phantom-computed-schema.sh "$$tmp/schema.json"; \
	  rc=$$?; rm -rf "$$tmp"; exit $$rc

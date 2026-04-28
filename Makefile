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
	    --google-out $(SCHEMAS_DIR)/google.filtered.json
	rm $(SCHEMAS_DIR)/.full.json

.PHONY: gen-imported
gen-imported: ## Regenerate typed resource files from filtered schemas.
	$(GO) run ./cmd/imported-codegen gen \
	    --aws-schema $(SCHEMAS_DIR)/aws.filtered.json \
	    --google-schema $(SCHEMAS_DIR)/google.filtered.json \
	    --out $(GEN_DIR)
	$(GO) build ./...

.PHONY: verify-gen
verify-gen: gen-imported ## Fail if regenerating produces a diff (CI gate).
	@if ! git diff --exit-code -- $(GEN_DIR) $(SCHEMAS_DIR); then \
	    echo ""; \
	    echo "==> Generated output is stale. Run 'make gen-imported' and commit."; \
	    exit 1; \
	fi

.PHONY: test
test: ## Run go test -race for the whole module.
	$(GO) test -race ./...

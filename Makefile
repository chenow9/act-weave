# ActWeave root Makefile — protocol generation and compatibility gates (M10-T1).

.PHONY: generate a2ui-generate a2ui-check protocol-compat-check protocol-baseline-accept protocol-check help

help:
	@echo "Targets:"
	@echo "  make generate                 Regenerate Schema Registry artifacts"
	@echo "  make a2ui-generate            Regenerate the TypeScript A2UI rendering contract"
	@echo "  make a2ui-check               a2ui-generate + git diff --exit-code"
	@echo "  make protocol-compat-check    Fail on breaking schema changes vs baseline"
	@echo "  make protocol-baseline-accept Accept current schemas as the new baseline"
	@echo "  make protocol-check           generate + git diff --exit-code + compat check"

generate:
	cd backend && go run ./cmd/protocolgen

a2ui-generate:
	cd backend && go run ./cmd/a2uigen

# Both renderers and the SDK hold a generated copy of the catalog contract and
# the shared fixtures. This fails if any copy drifts from the catalog.
A2UI_GENERATED = \
	demos/aap-chat/client/src/a2ui/generated \
	frontend/src/components/a2ui/generated \
	sdk/typescript/src/generated/a2ui.gen.ts \
	sdk/typescript/test/generated

a2ui-check: a2ui-generate
	@git diff --exit-code -- $(A2UI_GENERATED) \
		|| (echo "a2uigen produced a dirty tree; commit generated outputs" \
			&& git diff --stat -- $(A2UI_GENERATED) \
			&& exit 1)

protocol-compat-check:
	cd backend && go run ./cmd/protocolcompat -check \
		-report ../docs/verification/protocol-compat-report.md

protocol-baseline-accept:
	cd backend && go run ./cmd/protocolcompat -write-baseline

# CI / local acceptance for M10-T1.
protocol-check: generate
	@git diff --exit-code -- \
		backend/internal/protocolschema/schemas/aap/v1/SCHEMASET.sha256 \
		backend/internal/protocolschema/generated \
		sdk/typescript/src/generated \
		docs/openapi/generated \
		|| (echo "protocolgen produced a dirty tree; commit generated outputs" && git diff --stat && exit 1)
	$(MAKE) protocol-compat-check

# ActWeave root Makefile — protocol generation and compatibility gates (M10-T1).

.PHONY: generate protocol-compat-check protocol-baseline-accept protocol-check help

help:
	@echo "Targets:"
	@echo "  make generate                 Regenerate Schema Registry artifacts"
	@echo "  make protocol-compat-check    Fail on breaking schema changes vs baseline"
	@echo "  make protocol-baseline-accept Accept current schemas as the new baseline"
	@echo "  make protocol-check           generate + git diff --exit-code + compat check"

generate:
	cd backend && go run ./cmd/protocolgen

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

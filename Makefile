MODULES := bank partner lab
GOLANGCI_LINT_VERSION := v2.15.0
GOLANGCI_LINT := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

.PHONY: env up down logs test lint fmt check

env: ## generate .env with random secrets, if missing
	./entrypoint.sh --env-only

up: ## build and start the lab, waiting until every service is healthy
	./entrypoint.sh

down: ## stop the lab (keeps the database volume)
	docker compose down

logs:
	docker compose logs --follow

test: ## unit tests of every module, in both build variants
	@for module in $(MODULES); do \
		(cd $$module && go test -race ./... && go test -race -tags chaoslab ./...) || exit 1; \
	done

lint: ## golangci-lint on every module, in both build variants
	@for module in $(MODULES); do \
		(cd $$module && $(GOLANGCI_LINT) run ./... && $(GOLANGCI_LINT) run --build-tags chaoslab ./...) || exit 1; \
	done

fmt:
	@for module in $(MODULES); do \
		(cd $$module && $(GOLANGCI_LINT) fmt ./...) || exit 1; \
	done

check: ## safety checks: lab isolation and no chaos code in production binaries
	./scripts/check-lab-isolation.sh
	./scripts/check-production-binary.sh

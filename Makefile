.DEFAULT_GOAL := help

.PHONY: help
help: ## Print this help
	@echo "Usage: make [target]"
	@grep -E '^[a-z.A-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: test
test: ## Run tests
	@go test -v ./... -count=1

.PHONY: ensure-dir
ensure-dir:
	@mkdir -p bin

.PHONY: build
build: ensure-dir ## Build the binary
	@CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/argo-compare ./cmd/argo-compare

.PHONY: clean
clean: ## Remove build artifacts
	@rm -rf bin

.DEFAULT_GOAL := help

.PHONY: help
help: ## Print this help
	@echo "Usage: make [target]"
	@grep -E '^[a-z.A-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: mocks
mocks: ## Generate mocks
	@echo "===> Generating mocks"
	@mockgen --source=cmd/argo-compare/utils/interfaces.go --destination=cmd/argo-compare/mocks/interfaces.go --package=mocks

.PHONY: test
test: mocks ## Run tests
	@go test -v ./... -count=1

.PHONY: test-coverage
test-coverage: mocks ## Run tests with coverage
	@go test -v -coverprofile=coverage.out ./... -count=1 | go-junit-report -set-exit-code > report.xml

.PHONY: test-coverage-html
test-coverage-html: test-coverage ## Run tests with coverage and open HTML report
	@go tool cover -html=coverage.out -o coverage.html

.PHONY: ensure-dir
ensure-dir:
	@mkdir -p bin

.PHONY: build
build: ensure-dir ## Build the binary
	@CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/argo-compare ./cmd/argo-compare

.PHONY: clean
clean: ## Remove build artifacts
	@rm -rf bin

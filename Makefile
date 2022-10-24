.DEFAULT_GOAL := help

.PHONY: help
help: ## Print this help
	@grep -E '^[a-z.A-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'

.PHONY: test
test: ## Run tests
	@RLOG_LOG_LEVEL=NONE go test -v ./... -count=1

.DEFAULT_GOAL := help
SHELL := /bin/bash

TF      ?= tofu
INFRA   := infra
DIST    := dist
LEAGUE  ?= 1058423

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: test
test: ## Run unit tests
	go test ./...

.PHONY: check
check: ## Format check, vet and test
	@test -z "$$(gofmt -l .)" || { echo "unformatted files:"; gofmt -l .; exit 1; }
	go vet ./...
	go test ./...

.PHONY: build
build: ## Build the Lambda binary (linux/arm64)
	@mkdir -p $(DIST)
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc \
		-ldflags="-s -w" -o $(DIST)/bootstrap ./cmd/tick
	@ls -lh $(DIST)/bootstrap

.PHONY: preview
preview: ## Print the messages that would be sent right now, without sending
	go run ./cmd/preview -league $(LEAGUE)

.PHONY: capture
capture: ## Refresh the API fixtures from live responses
	./scripts/capture-fixtures.sh $(LEAGUE)

.PHONY: plan
plan: build ## Build then plan
	cd $(INFRA) && $(TF) init -upgrade && $(TF) plan

.PHONY: apply
apply: build ## Build then apply
	cd $(INFRA) && $(TF) init && $(TF) apply

.PHONY: destroy
destroy: ## Tear the stack down
	cd $(INFRA) && $(TF) destroy

.PHONY: invoke
invoke: ## Invoke the deployed Lambda once, now
	aws lambda invoke --function-name $$(cd $(INFRA) && $(TF) output -raw function_name) \
		--cli-binary-format raw-in-base64-out /dev/stdout

.PHONY: logs
logs: ## Tail the Lambda logs
	aws logs tail $$(cd $(INFRA) && $(TF) output -raw log_group) --follow

.PHONY: clean
clean: ## Remove build output
	rm -rf $(DIST)

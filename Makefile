.PHONY: help build run clean test compose-up compose-down compose-logs compose-ps compose-up-deps run-with-deps

APP_NAME := go-task-queue
CMD_PATH := ./cmd/go-task-queue
BIN_PATH := ./$(APP_NAME)
COMPOSE_FILE := ./deploy/docker-compose.yml

REDIS_ADDR ?= localhost:6379
HTTP_ADDR ?= :8080
WORKERS ?= 4
MONGODB_URI ?= mongodb://localhost:27017
HANDLERS_CONFIG ?= config/handlers.json

help: ## Show available targets
	@awk 'BEGIN {FS = ":.*##"; printf "\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  %-16s %s\n", $$1, $$2} END {printf "\n"}' $(MAKEFILE_LIST)

build: ## Build the local binary
	go build -o $(BIN_PATH) $(CMD_PATH)

run: build ## Build and run binary locally (requires Redis + MongoDB)
	REDIS_ADDR=$(REDIS_ADDR) \
	WORKERS=$(WORKERS) \
	HTTP_ADDR=$(HTTP_ADDR) \
	MONGODB_URI=$(MONGODB_URI) \
	HANDLERS_CONFIG=$(HANDLERS_CONFIG) \
	$(BIN_PATH)

clean: ## Remove local binary artifact
	rm -f $(BIN_PATH)

test: ## Run all Go tests
	go test ./...

compose-up: ## Start all services using deploy compose
	docker compose -f $(COMPOSE_FILE) up -d

compose-down: ## Stop and remove deploy compose services
	docker compose -f $(COMPOSE_FILE) down

compose-logs: ## Tail logs from all compose services
	docker compose -f $(COMPOSE_FILE) logs -f --tail=200

compose-ps: ## Show compose service status
	docker compose -f $(COMPOSE_FILE) ps

compose-up-deps: ## Start only Redis and MongoDB for local binary run
	docker compose -f $(COMPOSE_FILE) up -d redis mongo

run-with-deps: compose-up-deps run ## Start Redis/Mongo via compose, then run binary

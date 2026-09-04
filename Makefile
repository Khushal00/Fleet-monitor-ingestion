COMPOSE := docker compose -f ingestion/docker-compose.yml

.PHONY: test build deps down run run-ingestion run-serving schema seed health

test:
	cd ingestion && go test ./...
	cd serving && go test ./...

build:
	docker build -t fleet-monitor-ingestion:demo ./ingestion
	docker build -t fleet-monitor-serving:demo ./serving

deps:
	$(COMPOSE) up -d

down:
	$(COMPOSE) down

# Starts local TimescaleDB and Redis. Run the two service targets in separate terminals.
run: deps
	@printf '%s\n' 'Dependencies are running. Use `make run-ingestion` and `make run-serving` in separate terminals.'

run-ingestion:
	cd ingestion && go run .

run-serving:
	cd serving && go run .

schema:
	cd ingestion && go run ./scripts/init_db

seed:
	cd ingestion && go run ./scripts/seed_redis

health:
	curl --fail http://localhost:8001/health
	curl --fail http://localhost:8002/health

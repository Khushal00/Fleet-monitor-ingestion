# Fleet Monitor demo

Fleet Monitor is a two-service Go demo for receiving vehicle telemetry and
serving fleet, vehicle, trip, alert, and WebSocket views. It is intentionally
packaged for an interview demo, not a production certification.

```text
vehicle/device --POST /api/v1/telemetry--> ingestion :8001
                                             |        \
                                             v         v
                                    TimescaleDB + PostGIS   Redis
                                             ^         ^
                                             |         |
client/API/WebSocket <------------------- serving :8002
```

`ingestion` validates and admits telemetry, then writes historical data to
TimescaleDB and current state/authentication data to Redis. `serving` reads
both stores for APIs, WebSockets, and background monitors. Both services expose
`GET /health`, which reports the status of both dependencies.

## Local demo boot

Requirements: Go 1.25+, Docker with Compose, and `curl` for the optional
health check.

1. Prepare local configuration without overwriting any existing local files:

   ```sh
   test -f ingestion/.env || cp ingestion/.env.example ingestion/.env
   test -f serving/.env || cp serving/.env.example serving/.env
   ```

   Use the same local database password in both files and update
   `DB_PASSWORD` before running the applications. The compose file currently
   uses `fleet_password`; it is a disposable local-only default, not a cloud
   credential.

2. Start dependencies, create the schema, and seed Redis:

   ```sh
   make deps
   make schema
   make seed
   ```

3. In two terminals, start the services and check them:

   ```sh
   make run-ingestion
   make run-serving
   make health
   ```

   `make run` starts only the dependencies and prints the two service commands.
   Stop local dependencies with `make down`.

Useful packaging commands:

```sh
make test       # test both Go modules
make build      # build both container images
```

## Container images

Each service has a self-contained multi-stage Dockerfile:

```sh
docker build -t fleet-monitor-ingestion:demo ./ingestion
docker build -t fleet-monitor-serving:demo ./serving
```

The runtime images receive configuration only through environment variables;
they do not copy `.env` files. For a containerized local run, provide reachable
database and Redis addresses explicitly (inside a container, `localhost` is
the container itself).

## Render interview-demo setup

[`render.yaml`](render.yaml) declares two public Docker web services with
`/health` checks. It intentionally leaves all credentials and dependency
addresses as Render secrets (`sync: false`): supply them during initial
Blueprint creation, or add them in the Render dashboard for an existing
service. Set these for both services:

- `DB_HOST`, `DB_USER`, `DB_PASSWORD`, and `DB_NAME`
- `REDIS_ADDR` and, if used, `REDIS_PASSWORD`
- a fresh demo-only `VALID_API_KEYS` value, or leave it empty and run the
  Redis seed program to use the seeded device keys

Both Render services use port `10000` via `HTTP_PORT`; Render routes traffic to
that service port. After provisioning dependencies, run the schema and Redis
seed commands from a trusted one-off environment with the same variables:

```sh
cd ingestion && go run ./scripts/init_db
cd ingestion && go run ./scripts/seed_redis
```

Do not run these schema/data commands automatically at web-service startup:
they require deliberate credentials and should be controlled for a public
demo.

### Database compatibility is a deployment gate

The schema initializer requires **both TimescaleDB and PostGIS** extensions,
and also enables `btree_gist`. A generic managed PostgreSQL service is not
enough. Before selecting a Render-attached or external PostgreSQL provider,
verify that the chosen database version permits `CREATE EXTENSION timescaledb`,
`postgis`, and `btree_gist` for the demo database. Render is suitable for
hosting these Docker services, but its standard managed PostgreSQL offering
must not be assumed to supply TimescaleDB/PostGIS. Use a compatible managed
provider or self-managed PostGIS/TimescaleDB instance, then configure its host
and credentials in Render. Redis must be reachable from both web services; any
ephemeral/free-tier Redis data must be reseeded after reset.

## Demo endpoints

- Ingestion: `POST /api/v1/telemetry`, `GET /health`, `GET /metrics`
- Serving: `GET /health`, `GET /ws`, and `GET /api/v1/...`

Use only fresh demo credentials in public hosting. Keep provider secrets and
real device API keys outside the repository.

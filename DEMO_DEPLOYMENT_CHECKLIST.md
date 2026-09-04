# Fleet Monitor — Live Demo Deployment Checklist

This document tracks the work required to publish Fleet Monitor as an
interviewer-facing live demo. It is **not** an enterprise-production
certification checklist.

## Scope

- Target: a public, demonstrable deployment on a free-tier provider such as
  Render.
- Expected use: a short interview demonstration with seeded data and demo API
  keys.
- Out of scope: Kubernetes, multi-region high availability, Kafka/NATS,
  compliance programs, and enterprise disaster-recovery operations.

## Phase 1 — Current Code and Test Safety Net

- [ ] Record the final independent test-runner results.
- [ ] Review the test and minimal production-fix diff.
- [ ] Commit and push the test-safety-net work.
- [x] Backpressure now rejects overload instead of silently dropping queued
      telemetry behind `202 Accepted`.
- [x] Alert deduplication uses an atomic Redis claim and database backstop.
- [x] Serving fleet authorization fails closed for empty/unmapped fleet
      identities.
- [ ] Confirm that ingestion rejects a telemetry payload whose `fleet_id`
      differs from the Redis-mapped API-key fleet.
- [ ] Run final local smoke checks after the completed changes are committed.

## Phase 2 — Repository Deployment Packaging

- [ ] Add an `ingestion` Dockerfile.
- [ ] Add a `serving` Dockerfile.
- [ ] Add safe `.env.example` files containing placeholders only.
- [ ] Add a root `README.md` covering architecture, local boot, required
      variables, cloud setup, and the demo flow.
- [ ] Add a `Makefile` or equivalent task commands for test, build, run,
      schema initialization, and demo-data seeding.
- [ ] Remove the obsolete `version: '3.8'` line from the local Compose file.

## Phase 3 — Free-Tier Cloud Setup

- [ ] Select the provider and region (Render is the initial candidate).
- [ ] Create one public ingestion web service and one public serving web
      service.
- [ ] Provision a compatible PostgreSQL database with the required PostGIS and
      TimescaleDB capabilities.
- [ ] Provision Redis-compatible key-value storage.
- [ ] Verify database extension support *before* relying on the provider.
- [ ] Create fresh demo-only database credentials and API keys.
- [ ] Store values as provider environment variables/secrets; never commit or
      upload local `.env` files.
- [ ] Point both services at the hosted database and Redis instances.

### Render free-tier constraints to plan for

- Free web services can spin down after inactivity, causing a cold start.
- Free Postgres expires after 30 days and has limited storage.
- Free Key Value is in-memory, so its contents may disappear after restart.
- These limits are acceptable for a disposable interview demo, not customer
  fleet data.

## Phase 4 — Schema and Demo Data

- [ ] Run schema initialization against the hosted database.
- [ ] Run Redis fleet/API-key seed data.
- [ ] Add or document one repeatable demo-data seed command.
- [ ] Verify the hosted `/health` endpoints show database and Redis healthy.
- [ ] Keep sample requests and demo API keys in local/private notes, not in
      the repository.

## Phase 5 — Public Deployment Validation

- [ ] Deploy ingestion and confirm its public URL responds.
- [ ] Deploy serving and confirm its public URL responds.
- [ ] Send demo telemetry with a valid API key; expect `202` when admitted.
- [ ] Verify historical telemetry persists in PostgreSQL/TimescaleDB.
- [ ] Verify Redis live vehicle state updates.
- [ ] Trigger a speeding alert and verify it appears once.
- [ ] Verify a Fleet A key cannot read Fleet B data.
- [ ] Verify the WebSocket receives the correct fleet's events.
- [ ] Run a short public smoke check after deployment.

## Phase 6 — Interview Demo Script

- [ ] Wake the free services shortly before the interview to avoid cold-start
      delay.
- [ ] Open the serving dashboard/API view for the demo fleet.
- [ ] Send a normal telemetry event and show live state/history updates.
- [ ] Send unsafe telemetry and show the speeding alert.
- [ ] Acknowledge, resolve, and explain the alert lifecycle.
- [ ] Demonstrate Fleet A access and Fleet B rejection.
- [ ] Demonstrate a live WebSocket update, if the demo client uses one.
- [ ] Keep a fallback recording/screenshots in case the free-tier service is
      unavailable.

## Demo Go/No-Go

Deploy the live demo only after all applicable Phase 1-5 boxes are checked,
the hosted health checks are green, and the core telemetry/alert/authorization
flow has been exercised from the public URLs.

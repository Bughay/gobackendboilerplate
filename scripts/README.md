# End-to-End Demo Runner

`demo-all.sh` exercises the real HTTP API in one pass:

| Step | What is tested |
|---|---|
| 1 | Register a unique user (`201`, no password hash in the response) |
| 2 | Login and capture the access token (`200`, Bearer token) |
| 3 | Create two products, list them, fetch one, and update one |
| 4 | Create an order from two products and verify the server-computed total |
| 5 | Change a product price after the order, delete both products, and verify the order kept its name/price snapshots and set `product_id` to `null` |
| 6 | Enqueue the simple `demo:hello` job and poll it to completion |
| 7 | Enqueue the complex `demo:report` job, observe progress, the deliberate failure, the retry, and completion |

It does not start or stop containers. It only uses `curl` and `jq`, and it paces
requests to stay under the default 20 requests/second rate limit.

## Before running

You need the current `api` and `worker` services running, not the older
`backend-app` container that may still own port 8080.

```bash
cd backend

# If the old container is present, stop it.
docker stop backend-app

# Rebuild the current API and worker. One worker is easier to follow.
docker compose up -d --build --scale worker=1

docker compose ps
```

Open the two log streams in separate terminals:

```bash
# Terminal A
docker compose logs -f --tail=50 --no-log-prefix api

# Terminal B
docker compose logs -f --tail=50 --no-log-prefix worker
```

Your `.env` currently has `LOG_FORMAT=text`, so the logs are human-readable.

## Run

From `backend/`:

```bash
./scripts/demo-all.sh
```

The default complex job uses `fail_on_attempt=1`, so the script waits through
asynq's first retry backoff. That is normally **16-75 seconds**. The script keeps
polling and prints each state change.

For a faster run that skips the retry wait:

```bash
DEMO_FAIL_ON_ATTEMPT=0 ./scripts/demo-all.sh
```

## Expected output

The script prints sections `1/7` through `7/7`. Key checks:

```text
== 4/7 Orders ==
created order: {"id":"...","status":"pending","total":44,"items":[...]}
== 5/7 Order snapshots and product deletes ==
snapshot preserved after catalog update: total=44
order snapshot after deletes: {"total":44,"items":[...,{"product_name":"Demo Widget Pro","unit_price":12.25,"qty":2,"product_id":null},...]}
== 7/7 Complex job ==
  {"status":"queued","attempt":0,"progress":0,...}
  {"status":"running","attempt":1,"progress":20,...}
  {"status":"running","attempt":1,"progress":60,...}
  {"status":"retrying","attempt":1,"progress":60,...}
  (asynq retry backoff: ~16-75s before the next attempt)
  {"status":"running","attempt":2,"progress":20,...}
  {"status":"completed","attempt":2,"progress":100,"result":"report finished ..."}
```

In the API log you will see the enqueue and polling request lines. In the worker
log you will see `demo hello task received`, `demo hello completed`,
`demo report started`, progress lines, `demo report failed on purpose`, and the
second attempt.

## Environment overrides

| Variable | Default | Purpose |
|---|---|---|
| `BASE_URL` | `http://localhost:8080` | API base URL |
| `DEMO_PASSWORD` | `password123` | Password for the generated demo user |
| `DEMO_STEPS` | `5` | Steps for the complex job (1-20) |
| `DEMO_FAIL_ON_ATTEMPT` | `1` | `0` = no deliberate failure; `1` = fail attempt 1 and retry |
| `DEMO_JOB_TIMEOUT` | `180` | Seconds to wait for the complex job |
| `DEMO_REQUEST_DELAY` | `0.12` | Delay between API calls, to respect the rate limiter |

Examples:

```bash
BASE_URL=http://localhost:8080 ./scripts/demo-all.sh
DEMO_FAIL_ON_ATTEMPT=0 DEMO_REQUEST_DELAY=0 ./scripts/demo-all.sh
```

## What remains after the run

- The unique demo user and its order remain in Postgres.
- Both products are deleted on purpose, to verify order snapshots.
- The demo job state keys expire after one hour.

The script prints the user email, order ID, and job IDs at the end. Rerunning it
uses a new email, so runs do not collide.

## Troubleshooting

| Symptom | Cause |
|---|---|
| Preflight says the API is not healthy | The stack is not running, or `api` is not on port 8080. |
| Preflight probe returns `404` | The old `backend-app` image is serving. Stop it and rebuild the current `api`/`worker` services. |
| `HTTP 429` | The rate limiter tripped. The script retries once; increase `DEMO_REQUEST_DELAY` if you changed the limiter configuration. |
| Job stays `queued` | The worker is not running, or `WORKER_QUEUES` in `.env` does not include `default` and `critical`. |
| Job stays `retrying` for a minute | Expected: asynq's first retry backoff is roughly 16-75 seconds. |
| Job `GET` returns `404` | The worker could not reach Redis for state. Check the worker logs for `demo job state store unavailable`. |

## Removing the demo runner

The script is independent of the application:

```bash
rm -rf scripts/
```

Removing the runner does not affect register, login, products, orders, or the
demo job module. If you also remove the demo job module, delete the job-related
blocks listed in the package comment of `internal/modules/demo` and prune the
demo operations from `internal/platform/openapi/openapi.json`.

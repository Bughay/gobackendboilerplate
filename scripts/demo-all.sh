#!/usr/bin/env bash
#
# End-to-end demo for the boilerplate:
#   register -> login -> products CRUD -> order -> order snapshots -> jobs
#
# It only talks to the HTTP API. It never starts or stops containers.
#
# Usage:
#   ./scripts/demo-all.sh
#   DEMO_FAIL_ON_ATTEMPT=0 ./scripts/demo-all.sh     # skip the retry wait
#   BASE_URL=http://localhost:8080 ./scripts/demo-all.sh
#
# The script paces requests to stay below the default 20 req/s rate limit.
set -euo pipefail

BASE="${BASE_URL:-http://localhost:8080}"
BASE="${BASE%/}"

EMAIL="demo.$(date +%s).$$@example.com"
PASSWORD="${DEMO_PASSWORD:-password123}"
STEPS="${DEMO_STEPS:-5}"
FAIL_ON_ATTEMPT="${DEMO_FAIL_ON_ATTEMPT:-1}"
JOB_TIMEOUT="${DEMO_JOB_TIMEOUT:-180}"
REQUEST_DELAY="${DEMO_REQUEST_DELAY:-0.12}"

TOKEN=""
STATUS=""
BODY=""
SAW_RETRY=0

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

fail() {
  printf '\nFAIL: %s\n' "$*" >&2
  exit 1
}

section() {
  printf '\n== %s ==\n' "$*"
}

require() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

field() {
  jq -r "$1" <<<"$BODY"
}

# api METHOD PATH [JSON_BODY]
# Sends one request, stores the status in STATUS and the body in BODY.
# Retries once on 429 so a briefly saturated rate-limit window does not fail
# the demo.
api() {
  local method="$1" path="$2" body="${3:-}"
  local -a args=(-sS -X "$method" "$BASE$path"
    -H 'Accept: application/json'
    -o "$TMP_DIR/body.json"
    -w '%{http_code}')

  if [[ -n "$TOKEN" ]]; then
    args+=(-H "Authorization: Bearer $TOKEN")
  fi
  if [[ -n "$body" ]]; then
    args+=(-H 'Content-Type: application/json' --data "$body")
  fi

  local tries=0
  while :; do
    if ! STATUS=$(curl "${args[@]}"); then
      fail "curl failed for $method $path (is the API running at $BASE?)"
    fi
    if [[ "$STATUS" != "429" || "$tries" -ge 2 ]]; then
      break
    fi
    tries=$((tries + 1))
    sleep 1
  done

  if [[ -s "$TMP_DIR/body.json" ]]; then
    BODY=$(<"$TMP_DIR/body.json")
  else
    BODY=""
  fi

  # Stay below the default 20 requests/second per-IP limiter.
  sleep "$REQUEST_DELAY"
}

expect_status() {
  local want="$1" label="$2"
  if [[ "$STATUS" != "$want" ]]; then
    printf '\nFAIL: %s (HTTP %s, expected %s)\n' "$label" "$STATUS" "$want" >&2
    if [[ -n "$BODY" ]]; then
      printf '%s\n' "$BODY" | jq . >&2 2>/dev/null || printf '%s\n' "$BODY" >&2
    fi
    if [[ "$STATUS" == "404" && "$label" == *demo* ]]; then
      printf 'Hint: rebuild the current stack. The old backend-app image has no demo routes.\n' >&2
    fi
    exit 1
  fi
}

assert_json() {
  local filter="$1" label="$2"
  if ! jq -e "$filter" >/dev/null 2>&1 <<<"$BODY"; then
    printf '\nFAIL: %s\n' "$label" >&2
    printf '%s\n' "$BODY" | jq . >&2 2>/dev/null || printf '%s\n' "$BODY" >&2
    exit 1
  fi
}

# wait_for_job ID TARGET_STATUS TIMEOUT_SECONDS
# Polls until the job reaches the target status, printing only changed states.
# Sets SAW_RETRY=1 if a retrying state was observed.
wait_for_job() {
  local id="$1" want="$2" timeout="$3"
  local deadline=$((SECONDS + timeout))
  local last="" line status noted_retry=0
  SAW_RETRY=0

  printf 'polling job %s (timeout %ss)\n' "$id" "$timeout"
  while ((SECONDS < deadline)); do
    api GET "/demo/jobs/$id"
    expect_status 200 "poll job $id"

    status=$(field .status)
    line=$(jq -c '{status,attempt,progress,step,result,error}' <<<"$BODY")
    if [[ "$line" != "$last" ]]; then
      printf '  %s\n' "$line"
      last="$line"
    fi

    if [[ "$status" == "$want" ]]; then
      return 0
    fi
    if [[ "$status" == "failed" ]]; then
      fail "job $id reached failed: $line"
    fi
    if [[ "$status" == "retrying" ]]; then
      SAW_RETRY=1
      if [[ "$noted_retry" -eq 0 ]]; then
        printf '  (asynq retry backoff: ~16-75s before the next attempt)\n'
        noted_retry=1
      fi
    fi

    sleep 2
  done

  fail "job $id did not reach $want within ${timeout}s (last: $last)"
}

require curl
require jq

[[ "$FAIL_ON_ATTEMPT" =~ ^[01]$ ]] || fail "DEMO_FAIL_ON_ATTEMPT must be 0 or 1"
[[ "$STEPS" =~ ^[0-9]+$ ]] || fail "DEMO_STEPS must be an integer"
((STEPS >= 1 && STEPS <= 20)) || fail "DEMO_STEPS must be between 1 and 20"
[[ "$JOB_TIMEOUT" =~ ^[0-9]+$ ]] || fail "DEMO_JOB_TIMEOUT must be an integer"
((JOB_TIMEOUT > 0)) || fail "DEMO_JOB_TIMEOUT must be positive"
[[ "$REQUEST_DELAY" =~ ^[0-9]+([.][0-9]+)?$ ]] || fail "DEMO_REQUEST_DELAY must be a number"

printf 'End-to-end demo against %s\n' "$BASE"
printf 'A unique user will be registered: %s\n' "$EMAIL"
printf '\nWatch these two streams in separate terminals:\n'
printf '  docker compose logs -f --no-log-prefix api\n'
printf '  docker compose logs -f --no-log-prefix worker\n\n'

section "Preflight"
HEALTH=$(curl -sS -o /dev/null -w '%{http_code}' "$BASE/health" || true)
printf 'API health: %s\n' "$HEALTH"
[[ "$HEALTH" == "200" ]] || fail "API at $BASE is not healthy (HTTP $HEALTH). Start the compose stack first."

DEMO_PROBE=$(curl -sS -o /dev/null -w '%{http_code}' \
  "$BASE/demo/jobs/00000000000000000000000000000000" || true)
printf 'Demo route probe: %s (401 means the route exists and JWT is required)\n' "$DEMO_PROBE"
if [[ "$DEMO_PROBE" == "404" ]]; then
  fail "demo routes are missing. Rebuild the current stack and stop the old backend-app container."
fi

section "1/7 Register"
REGISTER_BODY=$(jq -nc --arg email "$EMAIL" --arg password "$PASSWORD" \
  '{email:$email,password:$password}')
api POST /auth/register "$REGISTER_BODY"
expect_status 201 "register"
assert_json '.success == true' "registration should return success=true"
[[ "$(field .person.email)" == "$EMAIL" ]] || fail "registered email does not match"
printf 'registered: %s (%s)\n' "$(field .person.email)" "$(field .person.id)"

section "2/7 Login"
LOGIN_BODY=$(jq -nc --arg email "$EMAIL" --arg password "$PASSWORD" \
  '{email:$email,password:$password}')
api POST /auth/login "$LOGIN_BODY"
expect_status 200 "login"
assert_json '.token_type == "Bearer"' "login should return a Bearer token"
TOKEN=$(field .access_token)
[[ -n "$TOKEN" && "$TOKEN" != "null" ]] || fail "login did not return an access token"
[[ "$(field .user.email)" == "$EMAIL" ]] || fail "login returned a different user"
printf 'token acquired (%d characters)\n' "${#TOKEN}"

section "3/7 Products"
api POST /products '{"name":"Demo Widget","qty":2,"price":9.99}'
expect_status 201 "create product 1"
P1=$(field .id)
assert_json '.price == 9.99 and .qty == 2' "product 1 should match its input"
printf 'created product 1: %s\n' "$(jq -c '{id,name,qty,price}' <<<"$BODY")"

api POST /products '{"name":"Demo Gadget","qty":5,"price":19.50}'
expect_status 201 "create product 2"
P2=$(field .id)
assert_json '.price == 19.5 and .qty == 5' "product 2 should match its input"
printf 'created product 2: %s\n' "$(jq -c '{id,name,qty,price}' <<<"$BODY")"

api GET /products
expect_status 200 "list products"
assert_json 'length == 2' "product list should contain two products"
printf 'product list: %s\n' "$(jq -c '[.[] | {name,qty,price}]' <<<"$BODY")"

api GET "/products/$P1"
expect_status 200 "get product 1"
assert_json '.name == "Demo Widget"' "get product 1 should return the created row"

api PUT "/products/$P1" '{"name":"Demo Widget Pro","qty":3,"price":12.25}'
expect_status 200 "update product 1"
assert_json '.name == "Demo Widget Pro" and .price == 12.25' "product 1 update should be visible"
printf 'updated product 1: %s\n' "$(jq -c '{name,qty,price}' <<<"$BODY")"

section "4/7 Orders"
ORDER_BODY=$(jq -nc --arg p1 "$P1" --arg p2 "$P2" \
  '{items:[{product_id:$p1,qty:2},{product_id:$p2,qty:1}]}')
api POST /orders "$ORDER_BODY"
expect_status 201 "create order"
ORDER=$(field .id)
assert_json '.status == "pending"' "new order should be pending"
assert_json '.items | length == 2' "order should contain two items"
assert_json '.total == 44' "order total should be 2*12.25 + 19.50 = 44"
printf 'created order: %s\n' "$(jq -c '{id,status,total,items:[.items[]|{product_name,unit_price,qty}]}' <<<"$BODY")"

api GET /orders
expect_status 200 "list orders"
assert_json 'length == 1' "order list should contain one order"

api GET "/orders/$ORDER"
expect_status 200 "get order"
assert_json '.total == 44' "fetched order total should be 44"

section "5/7 Order snapshots and product deletes"
# Change the catalog price after the order was created. The order must keep the
# price snapshot it took at creation time.
api PUT "/products/$P1" '{"name":"Demo Widget Pro","qty":3,"price":99.99}'
expect_status 200 "update product price after order"
api GET "/orders/$ORDER"
expect_status 200 "reload order after catalog change"
assert_json '.total == 44' "order total must not change when catalog price changes"
assert_json '[.items[] | select(.product_name == "Demo Widget Pro")] | length == 1' \
  "order should keep the product name snapshot"
assert_json '.items[] | select(.product_name == "Demo Widget Pro") | .unit_price == 12.25' \
  "order item should keep the original 12.25 unit price"
printf 'snapshot preserved after catalog update: total=%s\n' "$(field .total)"

api DELETE "/products/$P1"
expect_status 204 "delete product 1"
api DELETE "/products/$P2"
expect_status 204 "delete product 2"

api GET "/orders/$ORDER"
expect_status 200 "reload order after product deletes"
assert_json '.total == 44' "order total must survive product deletion"
assert_json '[.items[].product_id] | all(. == null)' \
  "deleted products should leave null product_id on order items"
assert_json '[.items[] | select(.product_name == "Demo Widget Pro")] | length == 1' \
  "order should still expose the product name snapshot"
printf 'order snapshot after deletes: %s\n' \
  "$(jq -c '{total,items:[.items[]|{product_name,unit_price,qty,product_id}]}' <<<"$BODY")"

api GET /products
expect_status 200 "list products after deletes"
assert_json 'length == 0' "product list should be empty after both deletes"

section "6/7 Simple job"
HELLO_BODY=$(jq -nc '{name:"Docker Demo"}')
api POST /demo/jobs/hello "$HELLO_BODY"
expect_status 202 "enqueue hello job"
HELLO_JOB=$(field .id)
printf 'hello job queued: %s (queue=%s)\n' "$HELLO_JOB" "$(field .queue)"
wait_for_job "$HELLO_JOB" completed 20
assert_json '.progress == 100' "hello job should finish at 100%"
assert_json '.result | contains("Docker Demo")' "hello result should contain the payload name"
HELLO_RESULT=$(field .result)
printf 'hello result: %s\n' "$HELLO_RESULT"

section "7/7 Complex job"
REPORT_BODY=$(jq -nc --argjson steps "$STEPS" --argjson fail "$FAIL_ON_ATTEMPT" \
  '{steps:$steps,fail_on_attempt:$fail,delay_seconds:0}')
api POST /demo/jobs/report "$REPORT_BODY"
expect_status 202 "enqueue report job"
REPORT_JOB=$(field .id)
printf 'report job queued: %s (queue=%s, steps=%s, fail_on_attempt=%s)\n' \
  "$REPORT_JOB" "$(field .queue)" "$STEPS" "$FAIL_ON_ATTEMPT"
wait_for_job "$REPORT_JOB" completed "$JOB_TIMEOUT"
assert_json '.progress == 100' "report job should finish at 100%"
assert_json '.result | contains("report finished")' "report result should be present"
REPORT_ATTEMPT=$(field .attempt)
REPORT_RESULT=$(field .result)
if [[ "$FAIL_ON_ATTEMPT" == "1" ]]; then
  # The attempt number is primary evidence of the retry. SAW_RETRY also covers
  # the case where polling happened to miss the short retrying state.
  if [[ "$SAW_RETRY" != "1" ]] && ((REPORT_ATTEMPT < 2)); then
    fail "expected the report job to retry (saw_retry=$SAW_RETRY attempt=$REPORT_ATTEMPT)"
  fi
fi
printf 'report result: %s (attempt %s)\n' "$REPORT_RESULT" "$REPORT_ATTEMPT"

section "Demo complete"
printf 'All checks passed.\n'
printf '  user:        %s\n' "$EMAIL"
printf '  order:       %s (total 44, snapshots verified)\n' "$ORDER"
printf '  hello job:   %s\n' "$HELLO_JOB"
printf '  report job:  %s (attempt %s)\n' "$REPORT_JOB" "$REPORT_ATTEMPT"
printf '\nThe demo user and order remain in Postgres. The two products were deleted\n'
printf 'to verify that order history keeps its name and price snapshots.\n'
printf '\nUseful commands:\n'
printf '  docker compose logs --tail=100 --no-log-prefix api\n'
printf '  docker compose logs --tail=100 --no-log-prefix worker\n'
printf '  docker compose exec -T redis redis-cli -n 0 GET "demo:job:%s" | jq\n' "$REPORT_JOB"

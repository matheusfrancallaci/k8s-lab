#!/usr/bin/env bash
set -euo pipefail

: "${TARGET_IMAGE:?TARGET_IMAGE is required}"
: "${EXPECTED_VERSION:?EXPECTED_VERSION is required}"
: "${ACR_NAME:?ACR_NAME is required}"

lease=/run/lab-busy
env_file=$(mktemp /run/lab-env.XXXXXX)
old_image=""

run_app() {
  local image=$1
  docker run -d --restart=unless-stopped \
    --security-opt no-new-privileges --cap-drop ALL \
    --network labnet --name lab \
    -p 127.0.0.1:8080:8080 \
    --env-file "$env_file" \
    -v lab-data:/app/data \
    "$image" >/dev/null
}

finish() {
  rm -f "$env_file" "$lease"
}

rollback() {
  local status=$?
  trap - EXIT
  if [ "$status" -ne 0 ] && [ -n "$old_image" ]; then
    docker rm -f lab >/dev/null 2>&1 || true
    run_app "$old_image" || true
  fi
  finish
  exit "$status"
}
trap rollback EXIT

touch "$lease"
timeout 90 az acr login --name "$ACR_NAME" >/dev/null
timeout 300 docker pull "$TARGET_IMAGE" >/dev/null

want=$(docker inspect --format '{{.Id}}' "$TARGET_IMAGE")
current=$(docker inspect --format '{{.Image}}' lab 2>/dev/null || true)
if [ "$want" = "$current" ]; then
  old_image=""
  finish
  trap - EXIT
  echo "MATCH $current"
  exit 0
fi

old_image=$current
test -n "$old_image"
docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' lab >"$env_file"
chmod 0600 "$env_file"

docker rm -f lab >/dev/null
run_app "$TARGET_IMAGE"

for attempt in $(seq 1 20); do
  health=$(curl -fsS --max-time 5 http://127.0.0.1:8080/healthz || true)
  if [ "$health" = "ok $EXPECTED_VERSION" ]; then
    break
  fi
  test "$attempt" -lt 20
  sleep 3
done

current=$(docker inspect --format '{{.Image}}' lab)
test "$want" = "$current"
old_image=""
finish
trap - EXIT
echo "MATCH $current"

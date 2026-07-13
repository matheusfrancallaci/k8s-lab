#!/usr/bin/env bash
set -euo pipefail

if systemctl is-enabled lab-autostop.timer >/dev/null 2>&1; then
  echo "lab-autostop.timer must be disabled for production availability" >&2
  exit 1
fi

test "$(docker inspect -f '{{.Config.Image}}' ollama)" = "ollama/ollama:0.30.11"

container_env=$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' lab)
expect_env() {
  key=$1
  value=$2
  grep -Fxq "${key}=${value}" <<<"$container_env"
}

test "$(docker inspect -f '{{.HostConfig.Privileged}}' lab)" = "false"
test "$(docker inspect -f '{{.HostConfig.SecurityOpt}}' lab)" = "[no-new-privileges]"
expect_env EMBEDDED_K3S 0

expect_env OLLAMA_MODEL qwen3:8b
expect_env OLLAMA_CHAT_MODEL qwen3:8b
expect_env OLLAMA_ROUTER_MODEL qwen3:4b
expect_env OLLAMA_GEN_MODEL qwen3:8b
expect_env OLLAMA_EMBED_MODEL embeddinggemma
expect_env OLLAMA_MAX_CONCURRENCY 1
expect_env OLLAMA_KEEP_ALIVE 15m
expect_env K8S_LAB_VERIFY_GENERATED 1
expect_env TUTOR_TELEMETRY_PERSIST 1
expect_env QUESTIONS_CUSTOM_DIR /app/data/questions-custom
expect_env LAB_SESSIONS_PATH /app/data/lab-sessions.json
expect_env TUTOR_CHECKPOINTS_PATH /app/data/tutor/checkpoints.json
grep -Eq '^DATABASE_URL=postgres://.+sslmode=require$' <<<"$container_env"

for _ in $(seq 1 30); do
  if models=$(docker exec ollama ollama list 2>/dev/null) \
    && grep -Eq '^qwen3:8b[[:space:]]' <<<"$models" \
    && grep -Eq '^qwen3:4b[[:space:]]' <<<"$models" \
    && grep -Eq '^embeddinggemma(:latest)?[[:space:]]' <<<"$models"; then
    echo RUNTIME_VERIFY_OK
    exit 0
  fi
  sleep 2
done

echo "configured Ollama models did not become ready" >&2
exit 1

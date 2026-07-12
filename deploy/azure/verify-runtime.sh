#!/usr/bin/env bash
set -euo pipefail

test "$(docker inspect -f '{{.Config.Image}}' ollama)" = "ollama/ollama:0.30.11"

container_env=$(docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' lab)
expect_env() {
  key=$1
  value=$2
  grep -Fxq "${key}=${value}" <<<"$container_env"
}

expect_env OLLAMA_MODEL qwen3:8b
expect_env OLLAMA_CHAT_MODEL qwen3:8b
expect_env OLLAMA_ROUTER_MODEL qwen3:4b
expect_env OLLAMA_GEN_MODEL qwen3:8b
expect_env OLLAMA_EMBED_MODEL embeddinggemma
expect_env OLLAMA_MAX_CONCURRENCY 1
expect_env OLLAMA_KEEP_ALIVE 15m
expect_env K8S_LAB_VERIFY_GENERATED 0
expect_env TUTOR_TELEMETRY_PERSIST 1
expect_env QUESTIONS_CUSTOM_DIR /app/data/questions-custom

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

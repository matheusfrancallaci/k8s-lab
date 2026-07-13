#!/usr/bin/env bash
set -euo pipefail

# cloud-init is applied only when the VM is created. Keep the existing VM in
# sync without replacing it or touching the APP_PASSWORD already stored here.
runtime=${RUNTIME_SCRIPT:-/opt/lab/run-lab.sh}
test -f "$runtime"
# A aplicacao publica deve permanecer disponivel. O antigo auto-stop derrubava
# tutor e labs durante sessoes sem terminal; economia passa a ser uma acao
# explicita de operador, nao uma indisponibilidade surpresa.
systemctl disable --now lab-autostop.timer >/dev/null 2>&1 || true
backup="${runtime}.bak"
cp -a "$runtime" "$backup"

rollback() {
  status=$?
  trap - EXIT
  if [ "$status" -ne 0 ]; then
    cp -a "$backup" "$runtime"
  fi
  exit "$status"
}
trap rollback EXIT

az login --identity --allow-no-subscriptions >/dev/null
key_vault=$(az keyvault list -g k8slab-rg --query '[0].name' -o tsv)
test -n "$key_vault"
export DATABASE_URL=$(az keyvault secret show --vault-name "$key_vault" --name database-url --query value -o tsv)
test -n "$DATABASE_URL"

python3 - "$runtime" <<'PY'
from pathlib import Path
import os
import re
import sys

path = Path(sys.argv[1])
content = path.read_text(encoding="utf-8")

content, image_changes = re.subn(
    r"(?m)^(?P<indent>[ \t]*)ollama/ollama(?::[^\s]+)?[ \t]*$",
    r"\g<indent>ollama/ollama:0.30.11",
    content,
)
if image_changes != 1:
    raise SystemExit(f"expected one Ollama image line, changed {image_changes}")

content, app_run_changes = re.subn(
    r"docker run -d(?: --privileged)? --restart=unless-stopped[ 	]*\\\r?\n[ 	]*(?:--security-opt no-new-privileges --cap-drop ALL[ 	]*\\\r?\n[ 	]*)?--network labnet --name lab",
    "docker run -d --restart=unless-stopped \\\n        --security-opt no-new-privileges --cap-drop ALL \\\n        --network labnet --name lab",
    content,
)
if app_run_changes != 1:
    raise SystemExit(f"expected one hosted app docker run, changed {app_run_changes}")

desired = [
	("EMBEDDED_K3S", "0"),
    ("OLLAMA_MODEL", "qwen3:8b"),
    ("OLLAMA_CHAT_MODEL", "qwen3:8b"),
    ("OLLAMA_ROUTER_MODEL", "qwen3:4b"),
    ("OLLAMA_GEN_MODEL", "qwen3:8b"),
    ("OLLAMA_EMBED_MODEL", "embeddinggemma"),
    ("OLLAMA_MAX_CONCURRENCY", "1"),
    ("OLLAMA_KEEP_ALIVE", "15m"),
    ("K8S_LAB_VERIFY_GENERATED", "0"),
    ("TUTOR_TELEMETRY_PERSIST", "1"),
    ("QUESTIONS_CUSTOM_DIR", "/app/data/questions-custom"),
	("LAB_SESSIONS_PATH", "/app/data/lab-sessions.json"),
	("TUTOR_CHECKPOINTS_PATH", "/app/data/tutor/checkpoints.json"),
	("DATABASE_URL", os.environ["DATABASE_URL"]),
]
key_pattern = "|".join(re.escape(key) for key, _ in desired)
content = re.sub(
    rf"(?m)^[ \t]*-e[ \t]+(?:{key_pattern})=.*?\\[ \t]*\r?\n?",
    "",
    content,
)

anchor = re.search(r"(?m)^[ \t]*-e[ \t]+OLLAMA_URL=.*?\\[ \t]*$", content)
if anchor is None:
    raise SystemExit("OLLAMA_URL anchor not found")

slash = chr(92)
block = "\n".join(
    f"        -e {key}='{value}' {slash}" for key, value in desired
)
content = content[: anchor.end()] + "\n" + block + content[anchor.end() :]
path.write_text(content, encoding="utf-8")
PY

chmod 0755 "$runtime"
bash -n "$runtime"
grep -q 'ollama/ollama:0.30.11' "$runtime"
for key in \
  EMBEDDED_K3S \
  OLLAMA_MODEL OLLAMA_CHAT_MODEL OLLAMA_ROUTER_MODEL OLLAMA_GEN_MODEL \
  OLLAMA_EMBED_MODEL OLLAMA_MAX_CONCURRENCY OLLAMA_KEEP_ALIVE \
  K8S_LAB_VERIFY_GENERATED TUTOR_TELEMETRY_PERSIST QUESTIONS_CUSTOM_DIR LAB_SESSIONS_PATH TUTOR_CHECKPOINTS_PATH DATABASE_URL; do
  test "$(grep -c -- "-e ${key}=" "$runtime")" -eq 1
done
! grep -q -- '--privileged' "$runtime"
grep -q -- '--security-opt no-new-privileges --cap-drop ALL' "$runtime"

if [ "${SYNC_RUNTIME_SKIP_PULL:-0}" != "1" ]; then
  timeout 900 docker pull ollama/ollama:0.30.11 >/dev/null
fi
rm -f "$backup"
trap - EXIT
echo RUNTIME_SYNC_OK

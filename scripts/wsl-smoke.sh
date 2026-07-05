#!/usr/bin/env bash
# Smoke test: roda o binário Linux dentro do WSL e confirma que serve HTTP.
set -u
cd /mnt/c/desenv/estudo-app || exit 3
echo "-- exec test --"
if [ ! -x ./estudo-app-linux ]; then
  chmod +x ./estudo-app-linux 2>/dev/null
fi
file ./estudo-app-linux 2>/dev/null | head -1
LAB_NO_CLUSTER=1 PORT=8097 ./estudo-app-linux >/tmp/wsl-app.log 2>&1 &
APP=$!
sleep 2
echo "-- curl de dentro do WSL --"
curl -s -o /dev/null -w "home     -> %{http_code}\n" http://localhost:8097/
curl -s -o /dev/null -w "favicon  -> %{http_code}\n" http://localhost:8097/static/favicon.svg
curl -s -o /dev/null -w "xterm.js -> %{http_code}\n" http://localhost:8097/static/vendor/xterm.js
kill $APP 2>/dev/null
echo "-- app log --"
cat /tmp/wsl-app.log

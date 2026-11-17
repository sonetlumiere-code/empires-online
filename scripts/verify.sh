#!/usr/bin/env bash
# Verificación completa (AGENTS.md §5): todo lo que comprueba la CI, en serie y
# esperando antes de cada paso a que no corra ninguna suite ajena (la regla de
# una sola carga pesada). Para en el primer fallo y muestra su salida.
#
# Necesita PostgreSQL y Redis de tests en marcha. Las URL se toman del entorno
# (EO_TEST_POSTGRES_URL, EO_TEST_REDIS_URL, EO_TEST_IDENTITY_POSTGRES_URL) y,
# si faltan, de los valores locales por defecto y del .env (sin mostrarlo).
#
#   pnpm run verify:full            # todo
#   pnpm run verify:full -- --desde go-test   # retomar desde un paso
set -uo pipefail

RAIZ="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GS="$RAIZ/services/game-server"
LOG="$(mktemp -t eo-verify)"
trap 'rm -f "$LOG"' EXIT

DESDE=""
[ "${1:-}" = "--" ] && shift
if [ "${1:-}" = "--desde" ]; then DESDE="${2:-}"; fi

# Node: el del PATH o, si no hay, el 22 de nvm.
if ! command -v node >/dev/null 2>&1; then
  for d in "$HOME"/.nvm/versions/node/v22.*/bin; do [ -x "$d/node" ] && PATH="$d:$PATH"; done
fi
command -v node >/dev/null || { echo "verify: no encuentro node" >&2; exit 1; }

: "${EO_TEST_POSTGRES_URL:=postgres://empires:empires_dev_password@localhost:5434/empires_test?sslmode=disable}"
: "${EO_TEST_REDIS_URL:=redis://localhost:6379/1}"
if [ -z "${EO_TEST_IDENTITY_POSTGRES_URL:-}" ] && [ -f "$RAIZ/.env" ]; then
  EO_TEST_IDENTITY_POSTGRES_URL="$(set -a; . "$RAIZ/.env" >/dev/null 2>&1; printf '%s' "${EO_TEST_IDENTITY_POSTGRES_URL:-}")"
fi
export EO_TEST_POSTGRES_URL EO_TEST_REDIS_URL EO_TEST_IDENTITY_POSTGRES_URL

calma() {
  if pgrep -f '[v]itest|[g]o test|\.tes[t] ' >/dev/null; then
    echo "   (suite en marcha; espero 20 s de calma)"
    local q=0
    while [ $q -lt 20 ]; do
      if pgrep -f '[v]itest|[g]o test|\.tes[t] ' >/dev/null; then q=0; else q=$((q + 1)); fi
      sleep 1
    done
  fi
}

saltando=0
[ -n "$DESDE" ] && saltando=1
paso() {
  local nombre="$1"; shift
  if [ $saltando -eq 1 ]; then
    [ "$nombre" = "$DESDE" ] && saltando=0 || { echo "== $nombre (saltado)"; return 0; }
  fi
  calma
  echo "== $nombre"
  local t0; t0=$(date +%s)
  if "$@" >"$LOG" 2>&1; then
    echo "   OK ($(( $(date +%s) - t0 )) s)"
  else
    echo "   FALLO: $nombre"
    tail -60 "$LOG"
    echo "VERIFICACIÓN: FALLO en $nombre (retomar con: pnpm run verify:full -- --desde $nombre)"
    exit 1
  fi
}

gofmt_vacio() { local f; f=$(gofmt -l .); [ -z "$f" ] || { echo "$f"; return 1; }; }
integracion_go() { EO_INTEGRATION=1 go test -count=1 -tags=integration ./...; }
integracion_web() {
  [ -n "$EO_TEST_IDENTITY_POSTGRES_URL" ] || { echo "falta EO_TEST_IDENTITY_POSTGRES_URL"; return 1; }
  (cd "$RAIZ/apps/web" && EO_INTEGRATION=1 pnpm exec vitest run src/server/store.integration.test.ts)
}
# Los canarios de .github/workflows/ci.yml: el build y el escaneo los ven en su entorno.
canarios() {
  EO_AUTH_JWT_SECRET=canario-ci-jwt-no-debe-llegar-al-navegador-01 \
  EO_IDENTITY_POSTGRES_URL=postgres://canario:canario-ci-identidad@canario.invalid:5432/empires_identity \
  EO_POSTGRES_URL=postgres://canario:canario-ci-juego@canario.invalid:5432/empires \
  EO_REDIS_URL=redis://:canario-ci-redis@canario.invalid:6379/0 "$@"
}

cd "$RAIZ"
paso check-docs node scripts/check-docs.mjs
paso lint-determinism node scripts/lint-determinism.mjs
paso scan-secrets-tree node scripts/scan-secrets.mjs --tree
paso scripts-test node --test "scripts/*.test.mjs"
cd "$GS"
paso go-build go build ./...
paso go-vet go vet ./...
paso go-vet-integration go vet -tags=integration ./...
paso gofmt gofmt_vacio
paso go-test go test -count=1 ./...
paso go-integration integracion_go
cd "$RAIZ"
paso web-integration integracion_web
paso typecheck pnpm run typecheck
paso protocol-test pnpm run protocol:test
paso protocol-check pnpm run protocol:check
paso web-test pnpm --filter @empires-online/web test
paso web-build canarios pnpm run web:build
paso scan-bundle canarios node scripts/scan-secrets.mjs --bundle apps/web/.next/static --require-canaries
echo "VERIFICACIÓN: OK"

#!/usr/bin/env bash
# Hook PreToolUse (Bash) de los agentes: antes de un `git commit`, comprobaciones
# ligeras de la Definition of Done (AGENTS.md §6). Lee el JSON de la llamada por
# stdin; si el comando no es un commit, sale en el acto. Bloquea (exit 2, con el
# motivo en stderr) si:
#   - scripts/check-docs.mjs falla;
#   - algún .go del commit no pasa gofmt;
#   - el commit cambia código de producción sin tocar ni un test ni docs/
#     (salvo con EO_SKIP_DOD=1 en el comando, justificado en el mensaje).
# Nada de suites aquí: la regla de una sola carga pesada (AGENTS.md §2).
set -uo pipefail

entrada="$(cat)"
comando="$(printf '%s' "$entrada" | python3 -c 'import json,sys
try:
    print(json.load(sys.stdin).get("tool_input",{}).get("command",""))
except Exception:
    print("")')"

# ¿Es un git commit? (también tras `cd … &&` o con `git -C dir commit`).
if ! printf '%s' "$comando" | grep -Eq '(^|[[:space:];&|(])git[[:space:]]+(-C[[:space:]]+[^[:space:]]+[[:space:]]+)?commit([[:space:]]|$)'; then
  exit 0
fi

raiz="${CLAUDE_PROJECT_DIR:-$(git rev-parse --show-toplevel 2>/dev/null)}"
[ -n "$raiz" ] && cd "$raiz" || exit 0

if ! command -v node >/dev/null 2>&1; then
  for d in "$HOME"/.nvm/versions/node/v22.*/bin; do [ -x "$d/node" ] && PATH="$d:$PATH"; done
fi

# Lo que entra en el commit. El hook corre ANTES del comando: si el mismo
# comando hace `git add` o `commit -a`, cuenta también lo que aún no está añadido.
ficheros="$(git diff --cached --name-only --diff-filter=ACMR)"
pendientes="$(printf '%s\n%s' "$(git diff --name-only --diff-filter=ACMR)" "$(git ls-files --others --exclude-standard)" | sed '/^$/d')"
# Las rutas que el mismo comando añade: TODO (git add -A/--all/., commit -a) o
# las que nombra cada `git add`, como prefijos.
rutas="$(printf '%s' "$comando" | python3 -c 'import re,shlex,sys
cmd=sys.stdin.read()
todo=bool(re.search(r"\bcommit\b[^;&|]*\s(-a|--all|-[a-zA-Z]*a[a-zA-Z]*)(\s|$)", cmd))
rutas=[]
for m in re.finditer(r"\bgit\s+(?:-C\s+\S+\s+)?add\b([^;&|]*)", cmd):
    try: args=shlex.split(m.group(1))
    except ValueError: args=m.group(1).split()
    for a in args:
        if a in ("-A","--all",".","-u","--update"): todo=True
        elif not a.startswith("-"): rutas.append(a.rstrip("/"))
print("*" if todo else "\n".join(rutas))')"
if [ "$rutas" = "*" ]; then
  ficheros="$(printf '%s\n%s' "$ficheros" "$pendientes" | sort -u | sed '/^$/d')"
elif [ -n "$rutas" ]; then
  extra="$(RUTAS="$rutas" python3 -c 'import os,sys
rutas=[r for r in os.environ["RUTAS"].splitlines() if r]
for f in sys.stdin.read().splitlines():
    if any(f == r or f.startswith(r + "/") for r in rutas): print(f)' <<<"$pendientes")"
  ficheros="$(printf '%s\n%s' "$ficheros" "$extra" | sort -u | sed '/^$/d')"
fi
[ -z "$ficheros" ] && exit 0

motivos=()

if command -v node >/dev/null 2>&1; then
  if ! salida="$(node scripts/check-docs.mjs 2>&1)"; then
    motivos+=("check-docs falla:"$'\n'"$(printf '%s' "$salida" | tail -15)")
  fi
fi

goes="$(printf '%s\n' "$ficheros" | grep -E '\.go$' || true)"
if [ -n "$goes" ] && command -v gofmt >/dev/null 2>&1; then
  sinformato=""
  while IFS= read -r f; do
    [ -f "$f" ] && [ -n "$(gofmt -l "$f")" ] && sinformato+="  $f"$'\n'
  done <<<"$goes"
  [ -n "$sinformato" ] && motivos+=("gofmt -l no sale vacío:"$'\n'"$sinformato")
fi

if ! printf '%s' "$comando" | grep -q 'EO_SKIP_DOD=1'; then
  produccion="$(printf '%s\n' "$ficheros" \
    | grep -E '^(services|apps|packages)/.*\.(go|ts|tsx|mjs|js|sql)$' \
    | grep -Ev '(_test\.go|\.test\.(ts|tsx|mjs|js)|/testdata/|/schema/v1/.*\.json)$' || true)"
  tests="$(printf '%s\n' "$ficheros" | grep -E '(_test\.go|\.test\.(ts|tsx|mjs|js))$|/testdata/' || true)"
  docs="$(printf '%s\n' "$ficheros" | grep -E '^docs/' || true)"
  if [ -n "$produccion" ] && [ -z "$tests" ] && [ -z "$docs" ]; then
    motivos+=("el commit cambia código de producción sin tocar ningún test ni docs/ (AGENTS.md §4):"$'\n'"$(printf '%s\n' "$produccion" | sed 's/^/  /' | head -10)"$'\n'"Añade los tests y las docs que pide el cambio, o, si de verdad no hacen falta, repite con EO_SKIP_DOD=1 delante de git commit y di por qué en el mensaje.")
  fi
fi

if [ ${#motivos[@]} -gt 0 ]; then
  {
    echo "Commit bloqueado por el hook de la Definition of Done (scripts/hooks/pre-commit-dod.sh):"
    for m in "${motivos[@]}"; do printf -- '- %s\n' "$m"; done
  } >&2
  exit 2
fi
exit 0

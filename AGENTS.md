# AGENTS.md — cómo se trabaja en Empires Online

Instrucciones para cualquier agente (y persona) que toque este repositorio. Lo que aquí se pide **es parte del
trabajo**, no un extra: una tarea no está hecha hasta cumplir §4 y §5, sin que nadie tenga que recordarlo. Lo propio de
una máquina concreta (puertos, rutas, `PATH`) va en `CLAUDE.local.md`, que no se versiona.

## 1. El proyecto en una pantalla

- MMO de estrategia persistente e isométrico. Game Server en Go (`services/game-server`, tick de 10 Hz, loop de un solo
  escritor), cliente Next.js 15 + PixiJS 8 (`apps/web`), protocolo en Zod (`packages/protocol`) exportado a JSON Schema
  y embebido en Go, PostgreSQL 16 + Redis.
- **Spec-Driven:** spec (`docs/specs`) → invariantes con ID (`docs/invariants`) → ADR si hace falta (`docs/decisions`)
  → código → tests → docs → CI verde. La Definition of Done es [docs/README.md](docs/README.md) §4.1; §4 de este fichero
  la hace concreta.
- **El estado vivo** (qué slices hay en `main`, qué falta) está en [docs/README.md](docs/README.md) §5,
  [docs/roadmap/milestones.md](docs/roadmap/milestones.md) y [docs/specs/README.md](docs/specs/README.md). El siguiente
  número libre de ADR y de migración se deduce de `docs/decisions/` y de `services/game-server/migrations/`.

## 2. Reglas (no negociables)

- **Quién decide.** Producto lo decide la persona usuaria; el diseño, el agente, dentro de las specs aceptadas. Una spec
  nueva nace en `Draft`, con sus preguntas de producto en §10 y la recomendación de cada una; **no se acepta ni se
  implementa** hasta que la persona decida.
- **Una sola carga pesada a la vez**, aunque sea de otro proyecto de la máquina. Antes de cada suite (Go, Vitest,
  `node --test`, typecheck), en un comando aparte: `pgrep -fl '[v]itest|[g]o test|\.tes[t] '`; si hay algo, esperar una
  ventana de ~20 s sin nada. Nunca `-race` en local (sólo la CI). `pnpm run verify:full` (§5) ya respeta esto.
- **No instalar nada** (paquetes, toolchains) sin visto bueno.
- **Secretos.** No pedir contraseñas; no escribir secretos salvo en `.env`; nunca mostrar el `.env` (cargarlo con
  `set -a; . ./.env` sin imprimirlo). Las credenciales de prueba son las del humo (`scripts/smoke.mjs`): no repetirlas en
  el chat.
- **Git.** Commits en español `tipo(ámbito): descripción`, con la línea `Co-Authored-By` del modelo; mensajes largos con
  `git commit -F` (fichero temporal). Cada slice en una rama `claude/slice-<nombre>` (comprobar antes que no exista).
  Sin push, merge ni PR salvo petición. Al fusionar: `merge --no-ff` con mensaje `merge: …`. Nunca restaurar con
  `git checkout` ni usar `git stash` a secas.
- **ADR aceptado = inmutable** ([docs/README.md](docs/README.md) §2.8): si la implementación concreta otro detalle, se
  anota como «Precisión (fecha)» en la spec.
- **La CI** no se toca sin visto bueno.
- **Aparcado hasta que haya jugadores:** la «dieta documental» (tablas de recuento antiguas), los tests de carga y el
  runbook 24/7.

## 3. El flujo de un slice

1. **Draft:** la spec con §1-§9 y las preguntas de §10 con recomendación. Los invariantes nuevos se nombran por familia
   (el check de docs no admite citar IDs sin registrar). Parar y esperar la decisión.
2. **Aceptación (un commit):** la spec a `Accepted` con §10 como decisiones; el ADR si lo hay; los invariantes nuevos
   registrados **sin cobertura** (ficha, fila del README de invariantes, recuentos); las precisiones de las reglas y los
   invariantes vecinos que cambian; el milestone abierto.
3. **Implementación** con sus tests (§4).
4. **Docs del slice** (§4.2).
5. **Verificación completa** (`pnpm run verify:full`) y mutaciones de los invariantes nuevos (§4.3).
6. **Prueba en vivo:** servidor + web + humo con su opción y, para lo visual, el navegador. Lo que no se pueda comprobar
   en el navegador se anota en milestones como «Sin comprobar en el navegador».
7. **Cierre en la rama:** precisiones de implementación en la spec (§9), frases que el slice deja falsas (buscar en
   `docs/product`, `docs/roadmap`, `docs/database`, `docs/architecture`, specs vecinas y los bloques de código de
   `docs/specs/websocket-protocol.md` §8), `docs/architecture/frontend.md`, cobertura de los invariantes.
8. **Tras fusionar, empujar y ver la CI en verde:** un commit de docs que pasa la spec a `Implemented` en la spec,
   `docs/specs/README.md`, milestones, backlog y `docs/README.md` §5, y el recuento de ADR de §2.8 si hubo uno nuevo.

Cada fallo encontrado: reproducirlo con un test al nivel que toque (y ver que falla sin el arreglo), corregirlo,
documentarlo como «Precisión (fecha)» si cambia una spec, y commitear.

## 4. Definition of Done, en concreto

### 4.1 Tests: qué nivel pide cada cambio

| Cambio | Tests |
|---|---|
| Regla de dominio pura | unit en `internal/domain/...` |
| Comando o comportamiento del tick | simulación (`internal/game/simulation`), con un golden con los ticks exactos si hay plazos; el catálogo de comandos y trabajos |
| Escritura o lectura en la base, migración, trigger, CHECK | integración (`-tags=integration`, `internal/persistence/postgres`) |
| Mensaje del protocolo o campo nuevo | contract en Go (`internal/protocol`, claves exactas contra el esquema) y en Zod (`packages/protocol`), y un e2e por WebSocket (`internal/websocket`) |
| Estado que sobrevive a un reinicio | recuperación (carga en la simulación y en la base) |
| Lógica del cliente | Vitest en `apps/web/src/state` |
| Opción nueva del humo | su función exportada con test en `scripts/smoke.test.mjs` |
| Código de error alcanzable | su test |

Convenciones de los tests que ya muerden:

- Antes de crear un ayudante en un paquete de tests, `grep` de su nombre: los nombres se repiten entre ficheros.
- En integración, `t.Context()` ya está cancelado dentro de `t.Cleanup` (usar `context.Background()` para limpiar); cada
  test toma un lock de la base: nunca dos `newTestStore` en el mismo test.
- En los e2e, `await`/`awaitWhere` consumen y descartan lo que saltan: esperar en el orden en que salen; «campo: null» se
  comprueba sobre el JSON crudo. Nunca leer la RAM de la simulación desde la goroutine de la conexión.
- Go serializa un slice `nil` como `null`: las listas obligatorias del esquema se inicializan vacías.
- «Verificado por mutación» sólo si se hizo la mutación y se vio fallar el test; restaurar desde una copia y comprobar
  con `cmp`, nunca con git. Una condición constante (`false &&`) no pasa el vet: usar una que no lo sea. Un trigger se
  desactiva con `ALTER TABLE … DISABLE TRIGGER` en la base de tests y se comprueba que vuelve a `tgenabled = 'O'`.

### 4.2 Docs: si cambias X, toca Y

| Si cambias… | …toca también |
|---|---|
| Un mensaje cliente→servidor o servidor→cliente, o un campo | Zod (`packages/protocol/src/v1`), `pnpm run protocol:build` (regenera los JSON Schema; antes de `go test`), Go (`internal/protocol/protocol.go`), `ClientMessageTypes` y su recuento en `contract_test.go`, `docs/specs/websocket-protocol.md` §7/§8 y su resumen de versiones, `docs/specs/README.md` |
| Un campo **obligatorio** en `CityView`, `TerritoryView`, `ClanView`… | los fixtures del cliente y del protocolo (`grep` del campo vecino en `apps/web/src` y `packages/protocol`); se estrena junto en cliente y servidor |
| Un valor nuevo en un enum estricto | lo mismo: rompe a un cliente viejo |
| Un evento en `world_events` entre dos jugadores | `counterpart_player_id` y `atMs` (ADR-028); si va al registro, `eventlog.EventTypes`, `Classify` y el texto en `apps/web/src/state/eventLog.ts`; si se avisa, `eventlog.NoticeKinds` y `EventLogNoticeKind` de Zod; `docs/specs/event-log.md` y `live-notices.md` |
| Un trabajo de la cola o un comando de la simulación | `simulation.JobNames` / `CommandTypes` y la clasificación de `actions.go` |
| Una tabla | el `TRUNCATE` de `internal/persistence/postgres/testenv_integration_test.go` y `docs/database/schema.md` §1 y su ficha |
| Una migración | el par `up`/`down`; si ya está aplicada en la base de tests, deshacerla con su `.down.sql` antes de cambiarla |
| Una variable `EO_` | `internal/config/config.go`, `.env.example` y `docs/operations/configuration.md` |
| Una métrica o una serie | `internal/observability` (series a cero desde el arranque) y `docs/operations/monitoring.md` §2 |
| Un recurso, un edificio o un tipo de unidad | sus catálogos (`economy.Resources()`, `BuildingType`, `UnitType`), sus `CHECK` y Zod |
| Una regla de paz o de permiso | `State.AtPeace` y el motivo de cada rechazo en combate, saqueo, conquista y reclamación |
| El cliente | `docs/architecture/frontend.md` |
| Un invariante | su ficha en `docs/invariants/<familia>.md`, la fila y los recuentos del README de invariantes |
| Una spec | su estado en `docs/specs/README.md`; las precisiones, como «Precisión (fecha)» |

`node scripts/check-docs.mjs` comprueba enlaces, anclas, invariantes y ADR; el hook de §6 lo pasa en cada commit.

### 4.3 Invariantes

Cada invariante nuevo nace sin cobertura en la aceptación y pasa a ✓ sólo con tests que existen y pasan; los nuevos se
verifican por mutación (§4.1) y la ficha dice qué mutación se hizo y qué test cayó.

## 5. Verificación

`pnpm run verify:full` corre, en serie y esperando a que no haya suites ajenas, todo lo que la CI comprueba: docs, lint de
determinismo, secretos, tests de los scripts, build/vet/gofmt/test de Go, la integración de Go y la de la web, el
typecheck, los tests y el check del protocolo, los tests del cliente, el build de la web con los canarios de
`.github/workflows/ci.yml` y el escaneo del bundle. Necesita PostgreSQL y Redis de tests en marcha (ver
`CLAUDE.local.md` o [docs/operations/local-development.md](docs/operations/local-development.md)). Nada se da por hecho
sin que pase entero. Tras cambiar Zod, `pnpm run protocol:build` antes.

## 6. El hook de commit

`.claude/settings.json` engancha `scripts/hooks/pre-commit-dod.sh` antes de cada `git commit` que lance un agente:

- comprueba `check-docs`, el escaneo de secretos de los ficheros del commit (`scan-secrets.mjs --files`) y
  `gofmt -l` de sus ficheros Go;
- **bloquea** un commit que cambia código de producción (`services/`, `apps/`, `packages/`) sin tocar ningún test ni
  ningún fichero de `docs/`.

Si un commit de código de verdad no necesita ni tests ni docs (un renombre interno, un comentario), se lanza con
`EO_SKIP_DOD=1 git commit …` y el mensaje del commit dice por qué. No es una vía para saltarse §4.

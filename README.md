# Empires Online

MMORTS persistente 24/7 con servidor autoritativo, mundo continuo y vista isométrica.
Inspirado conceptualmente en los RTS clásicos (eras, civilizaciones) y en los MMORPG
isométricos de mundo persistente (riesgo, comunidad, consecuencias de la ausencia).

**El mundo sigue existiendo aunque cierres el navegador.** Las unidades terminan sus
recorridos, los cooldowns corren y las ciudades cambian de estado mientras nadie mira.

> La documentación completa vive en [`docs/`](docs/README.md). Este archivo sólo explica
> cómo poner el proyecto en marcha.

---

## Los cuatro principios que no se negocian

1. **El servidor es la única autoridad.** El cliente envía intenciones; el servidor decide
   la verdad. Nunca se acepta del cliente una posición, un HP, un resultado ni una ruta.
2. **El mundo es persistente.** Ningún estado durable depende de que haya un WebSocket vivo.
3. **Estar desconectado no detiene el mundo.** Sólo cambian las reglas ligadas a tu presencia.
4. **Cada capa de estado tiene su papel.** PostgreSQL es la verdad durable, Redis es estado
   caliente y prescindible, la RAM del servidor es la simulación activa.

Ver [`docs/architecture/overview.md`](docs/architecture/overview.md).

---

## Estructura del repositorio

```
empires-online/
├── apps/web/              Cliente: Next.js 15 + React 19 + PixiJS 8
├── packages/protocol/     Contrato WebSocket (Zod) → JSON Schema para ambos lados
├── services/game-server/  Game Server autoritativo en Go: game loop, mundo, persistencia
├── infra/docker/          Dockerfile del Game Server
├── docs/                  Especificaciones, arquitectura, invariantes, ADR, operaciones
└── docker-compose.yml     PostgreSQL + Redis para desarrollo local
```

---

## Puesta en marcha

### Requisitos

| Herramienta | Versión | Comprobar |
|---|---|---|
| Go | 1.23 o superior | `go version` |
| Node.js | 22 o superior | `node --version` |
| pnpm | 10 o superior | `pnpm --version` |
| Docker Desktop | con el daemon **arrancado** | `docker info` |

En Windows, Go se instala con `winget install --id GoLang.Go`.

### 1. Infraestructura

```bash
pnpm install
pnpm run db:up          # PostgreSQL 16 + Redis 7 en contenedores
```

`db:up` falla si el daemon de Docker no está en marcha. En Windows hay que abrir
Docker Desktop (requiere permisos de administrador la primera vez).

<details>
<summary><b>Alternativa sin Docker</b> (cluster propio, sin permisos de administrador)</summary>

Docker Desktop necesita elevación para arrancar su servicio, y una instancia de
PostgreSQL preinstalada suele traer una contraseña de superusuario que nadie
recuerda. Ambos problemas desaparecen creando un cluster propio: quien lo crea
es su superusuario.

```bash
pnpm run pg:init      # crea el cluster en .pgdata/ y lo arranca en el puerto 5433
```

Sólo necesita los binarios de PostgreSQL instalados (`initdb`, `pg_ctl`); no
instala ningún servicio ni toca la instancia que ya tengas. Después:
`pg:start`, `pg:stop`, `pg:status` y `pg:destroy`.

En el `.env`, apunta a ese puerto y deja Redis vacío:

```
EO_POSTGRES_URL=postgres://empires:empires_dev_password@localhost:5433/empires?sslmode=disable
EO_REDIS_URL=
```

Sin `EO_REDIS_URL`, el servidor usa **estado caliente en proceso**: da las
mismas garantías que Redis mientras haya una sola instancia, y la configuración
lo rechaza en producción. Aplica el esquema con `pnpm run db:migrate`.

Si en cambio prefieres usar un PostgreSQL que ya administras, `psql -U postgres
-f scripts/setup-local-db.sql` crea el rol y las bases en él.

</details>

### 2. Configuración

```bash
cp .env.example .env     # PowerShell: Copy-Item .env.example .env
```

Los valores por defecto sirven tal cual para desarrollo local. La referencia completa
está en [`docs/operations/configuration.md`](docs/operations/configuration.md).

### 3. Protocolo

```bash
pnpm run protocol:build  # Zod → JSON Schema, para packages/protocol y para el servidor Go
```

### 4. Game Server

```bash
cd services/game-server
go run ./cmd/server
```

Al arrancar aplica las migraciones, genera el mundo desde la semilla, rehidrata las
unidades y reanuda los movimientos que quedaron a medias. Escucha en `:8080`
(juego) y `:9090` (métricas).

### 5. Cliente

```bash
pnpm run web:dev         # http://localhost:3000
```

Pulsa **Fundar imperio** para crear un jugador con su ciudad y sus tres aldeanos.
Haz clic en un aldeano para seleccionarlo y en el mapa para ordenarle moverse.

---

## Comandos

| Comando | Qué hace |
|---|---|
| `pnpm run db:up` / `db:down` / `db:reset` | Infraestructura en Docker (`db:reset` **borra los datos**) |
| `pnpm run db:psql` / `db:redis` | Consola de PostgreSQL o Redis dentro del contenedor |
| `pnpm run pg:init` / `pg:start` / `pg:stop` / `pg:status` | Cluster de PostgreSQL propio, sin Docker ni administrador |
| `pnpm run db:migrate` / `db:version` | Aplica el esquema / muestra la versión aplicada |
| `pnpm run docs:check` | Verifica enlaces, invariantes registrados y ADR citados |
| `pnpm run protocol:build` | Regenera el JSON Schema desde los esquemas Zod |
| `pnpm run protocol:check` | Falla si el JSON Schema ha derivado (lo mismo que hace la CI) |
| `pnpm run server:test` | Tests unitarios del Game Server |
| `pnpm run server:test:integration` | Tests contra PostgreSQL y Redis reales |
| `pnpm run web:dev` / `web:build` | Cliente en desarrollo / build de producción |
| `pnpm run verify` | Todo lo anterior de una vez: la comprobación previa a una PR |

No hay `Makefile`: el task runner es pnpm, que ya está disponible en todas las
plataformas donde se desarrolla el proyecto.

---

## Tests

```bash
pnpm run protocol:test                    # contrato del protocolo (TypeScript)
pnpm --filter @empires-online/web test    # coordenadas, interpolación, store, cliente WS
cd services/game-server && go test ./...  # dominio, pathfinding, simulación, auth, config
```

Los tests de integración necesitan la infraestructura arrancada y se activan
explícitamente:

```bash
pnpm run db:up
cd services/game-server
EO_INTEGRATION=1 go test -tags=integration ./...
```

Sin `EO_INTEGRATION=1` se **saltan** en lugar de fallar: un test que no puede
ejecutarse debe decirlo, no fingir que el código está roto.

La estrategia completa está en [`docs/testing/strategy.md`](docs/testing/strategy.md).

---

## Metodología

Este proyecto se desarrolla con **Spec-Driven Development**: primero la
especificación y sus invariantes, después la implementación, después los tests, y
sólo entonces se considera terminada una feature.

```
Requirement → Specification → Invariants → Architecture → Implementation
           → Unit → Integration → Contract → Documentation → Review
```

Ninguna feature importante se implementa a partir de una descripción informal.
Las decisiones que condicionan la arquitectura se registran como ADR en
[`docs/decisions/`](docs/decisions/README.md).

La *Definition of Done* completa está en [`docs/README.md`](docs/README.md).

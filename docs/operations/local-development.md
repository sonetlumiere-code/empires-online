# Entorno de desarrollo local

Guía reproducible para levantar Empires Online en una máquina de desarrollo: infraestructura en Docker, Game Server en Go, paquete de protocolo, migraciones y tests.

> Los comandos `pnpm run …` que aparecen aquí son **los scripts reales de `package.json`**
> (ver [Tabla de scripts](#8-tabla-de-scripts-pnpm)); no existe Makefile porque `make` **no está instalado**
> en la máquina de desarrollo Windows.
>
> `apps/web/` (el frontend Next.js) **ya existe** en el repositorio: Next.js 15 + React 19 + PixiJS 8,
> con 58 tests de Vitest en verde y `next build` correcto. `pnpm run web:dev` lo levanta en
> `http://localhost:3000`.

---

## 1. Hechos del entorno detectado

Lo siguiente son hechos verificados en esta máquina, no supuestos. Cualquier desviación debe corregirse antes de continuar.

| Herramienta | Estado | Versión detectada | Acción |
|---|---|---|---|
| Windows 10 Pro | OK | 10.0.19045 | — |
| Node.js | OK | v22.17.1 | — |
| pnpm | OK | 10.25.0 | — |
| git | OK | 2.38.1 | — |
| Go | **OK, instalado** | 1.27.0 en `C:\Program Files\Go` | — (§2.1) |
| Docker CLI | OK | 20.10.22 | — |
| Docker Compose | OK | v2.15.1 (plugin `docker compose`) | — |
| Docker daemon | **NO SE USA** | — | Decisión, no limitación: esta máquina produce pantallazos azules por consumo de RAM y Docker consume demasiada. Tampoco llegó nunca a arrancar (servicio en *Manual*, exige elevación). El camino real es §3-bis |
| PostgreSQL 16 | **OK, instalado** | servicio `postgresql-x64-16` en el puerto 5432 | No se usa: su contraseña de superusuario se desconoce. §3-bis crea un cluster propio |
| `psql`, `initdb`, `pg_ctl` | **OK** | `C:\Program Files\PostgreSQL\16\bin` | Disponibles; no están en el `PATH` por defecto |
| WSL | **OK** | Ubuntu 22.04.1 LTS, **WSL 1** | Es el Linux donde corren `-race` y Redis (§3-bis.7) |
| gcc | **sólo en WSL** | 11.4.0 (ausente en Windows) | Sin él `-race` aborta con `-race requires cgo` |
| Go dentro de WSL | **OK** | 1.27.0 en `$HOME/golang` | Instalado por tarball; el `apt` de Ubuntu 22.04 trae 1.18 y `go.mod` exige 1.25.11 |
| Redis | **OK, en WSL** | 6.0.16 (la CI usa `redis:7-alpine`) | En Windows no hay; sin `EO_REDIS_URL` se usa estado caliente en proceso (§3-bis.2) |
| `make` | NO INSTALADO | — | No se usará: task runner = pnpm scripts |
| `gh` | **OK, instalado** | 2.100.0 (ámbito de usuario) | **Sin sesión iniciada**: `git push` usa Git Credential Manager, que es independiente |

Consecuencias directas, que conviene interiorizar antes de empezar:

- **El camino con Docker (§3 y §4) no es una opción en esta máquina.** No está ahí como alternativa
  equivalente sino como referencia para otros entornos. El camino real es §3-bis.
- **Los tests de integración de PostgreSQL están ejecutados y en verde** (12 tests) por el camino de
  §3-bis, tanto desde Windows como desde WSL contra el mismo cluster.
- **Los tests de integración de Redis también** (9 tests, §3-bis.7), ejecutados de verdad y no saltados.
  Los contratos de `internal/persistence/memory` siguen cubriendo lo mismo de forma determinista con un
  reloj falso, que es lo que permite trabajar sin arrancar WSL.
- **El detector de carreras pasa** sobre los 10 paquetes con tests, desde WSL. Es lo que respalda la
  afirmación de que el bucle de juego está aislado por diseño y no necesita mutexes; en Windows no puede
  ejecutarse.
- **La CI se ejecuta.** El repositorio es público en
  [sonetlumiere-code/empires-online](https://github.com/sonetlumiere-code/empires-online) y Actions
  corre sobre cada push a `main`. Estuvo un tiempo bloqueada por facturación mientras el repositorio
  era privado: los jobs se rechazaban **antes de arrancar**, lo cual no era un fallo del código sino
  la ausencia de cualquier ejecución.
- **La construcción de la imagen del contenedor sólo se verifica en la CI**, porque no tiene sustituto
  local: construir una imagen exige un daemon y Docker está descartado en esta máquina. Ya no es una
  afirmación sin evidencia — el job la construye—, pero sigue siendo lo único del proyecto que nadie
  puede comprobar desde aquí.
- **No existe `make`.** Si un documento, script o hilo de CI referencia `make <target>`, es un error: el
  equivalente es `pnpm run <script>`.

---

## 2. Prerrequisitos

### 2.1 Go (ya instalado: 1.27.0)

El Game Server vive en `services/game-server` (módulo
`github.com/empires-online/empires-online/services/game-server`). La directiva `go` de su `go.mod` fija la
versión **mínima** del módulo — hoy `go 1.25.11` —; el toolchain instalado en esta máquina es **Go 1.27.0**,
que la satisface de sobra.

> **Divergencia detectada y corregida.** El workflow de CI fijaba `GO_VERSION: '1.23'`, por debajo de la
> directiva de `go.mod`, así que no habría compilado. La causa de fondo es que el mínimo no lo elige el
> proyecto: lo imponen las dependencias —`pgx/v5` y `prometheus/client_golang` declaran `go 1.25.0`—, y
> `go mod tidy` sube la directiva sola. Bajarla a mano no sirve: el siguiente `tidy` la vuelve a subir.
>
> La corrección es eliminar la duplicación, no ajustar los dos números. La CI ya no fija ninguna versión:
> usa `go-version-file: services/game-server/go.mod`, de modo que el único sitio donde vive el dato es
> `go.mod`. La imagen de `infra/docker/game-server.Dockerfile` se alineó a `golang:1.25-alpine`.

Se instaló con:

```powershell
winget install --id GoLang.Go
```

Tras la instalación hay que cerrar y reabrir la terminal para que `PATH` recoja `C:\Program Files\Go\bin`.
Verificación:

```powershell
go version
# observado: go version go1.27.0 windows/amd64
go env GOPATH GOMODCACHE
```

Si en una terminal nueva `go` no responde, el problema es el `PATH`, no la instalación. Si hiciera falta
reinstalar y `winget` no estuviera disponible, sirve el instalador MSI oficial de `https://go.dev/dl/`.

**Linux/macOS:**

```bash
# macOS (Homebrew)
brew install go
# Linux (tarball oficial)
curl -fsSLO https://go.dev/dl/go1.27.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.27.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
go version
```

**Todos los comandos de Go se ejecutan desde `services/game-server`**, porque ahí está el `go.mod`. Los
scripts de pnpm (`server:build`, `server:run`, `server:test`, `server:vet`, `server:fmt`) ya hacen ese `cd`
por ti; prefiérelos a invocar `go` a mano desde la raíz.

### 2.2 Node 22 y pnpm 10 (ya presentes)

```powershell
node --version    # v22.17.1
pnpm --version    # 10.25.0
```

Si `pnpm` no estuviera en el `PATH` de una terminal nueva, habilitarlo con Corepack:

```powershell
corepack enable
corepack prepare pnpm@10.25.0 --activate
```

### 2.3 Docker Desktop

Docker Desktop **está instalado** (CLI 20.10.22, Compose v2.15.1), pero en esta máquina **no se usa**:
produce pantallazos azules por consumo de RAM. Tampoco llegó a arrancar el daemon. No es un
prerrequisito pendiente: es una vía descartada. El camino que funciona aquí es §3-bis.

---

## 3. Arrancar el daemon de Docker y comprobar que está vivo

> **Esta sección no aplica a la máquina de desarrollo actual.** Docker está descartado aquí (§1 y §2.3).
> Se conserva porque el `docker-compose.yml` y el `Dockerfile` del repositorio siguen siendo válidos en
> otros entornos y en la CI, y porque el diagnóstico de abajo es el correcto cuando alguien se encuentre
> el daemon apagado. Si estás trabajando en esta máquina, salta a **§3-bis**.

Este es el fallo número uno del entorno local. La CLI responde aunque el daemon esté apagado, así que
`docker --version` **no** es una comprobación válida.

### 3.1 Arrancar Docker Desktop (Windows)

```powershell
Start-Process "C:\Program Files\Docker\Docker\Docker Desktop.exe"
```

Espera a que el icono de la bandeja quede en estado *Running*: en frío tarda 30–90 s, y durante ese rato
`docker info` sigue fallando. No es un error, es el arranque.

En Linux: `sudo systemctl start docker`. En macOS: abrir Docker Desktop desde Launchpad.

Para que no haya que repetirlo cada día, en Docker Desktop: *Settings → General → Start Docker Desktop when
you sign in to your computer*.

### 3.2 Comprobar que el daemon responde

La comprobación válida —la única— es:

```powershell
docker info
```

- Si responde con una sección `Server:` y datos del servidor (versión, número de contenedores,
  *Storage Driver*) → **daemon vivo**, puedes continuar.
- Si responde `error during connect: ... The system cannot find the file specified.` o
  `Cannot connect to the Docker daemon` → **daemon apagado**: vuelve a §3.1 y espera.

Comprobación abreviada, apta para un script o un hook previo a los tests:

```powershell
docker info --format '{{.ServerVersion}}'    # imprime la versión del servidor, o falla con código != 0
```

```bash
docker info >/dev/null 2>&1 && echo "daemon OK" || echo "daemon APAGADO"
```

Solo cuando `docker info` responda tiene sentido ejecutar `pnpm run db:up`.

---

## 3-bis. Camino alternativo: sin Docker y sin permisos de administrador

Docker no siempre está disponible, y en esta máquina fue justamente el caso: el servicio
`com.docker.service` está en *Manual* y arrancarlo exige elevación. Este camino evita Docker por
completo y **está verificado de extremo a extremo**: con él se aplicaron las migraciones por primera
vez, se ejecutaron los tests de integración y se jugó el vertical slice completo.

Necesita únicamente los binarios de PostgreSQL (`initdb`, `pg_ctl`, `createdb`) instalados. No
instala ningún servicio, no pide contraseñas ajenas y no toca ninguna otra instancia de PostgreSQL
que ya tengas.

### 3-bis.1 Un cluster propio de PostgreSQL

```bash
pnpm run pg:init
```

Crea un cluster en `.pgdata/` (ignorado por git), lo configura en el **puerto 5433** escuchando solo
en loopback, lo arranca y crea las bases `empires` y `empires_test`.

La clave de por qué esto resuelve el problema de credenciales: **quien ejecuta `initdb` es el
superusuario del cluster que crea**. No hay ninguna contraseña previa que averiguar. El rol se llama
`empires` y su contraseña es la que ya documenta `.env.example`.

| Comando | Efecto |
|---|---|
| `pnpm run pg:init` | Crea el cluster y las bases. Falla sin destruir nada si ya existe. |
| `pnpm run pg:start` / `pg:stop` | Arranca / detiene (`stop` usa modo `fast`: cierra conexiones y hace checkpoint). |
| `pnpm run pg:status` | Dice si está en marcha y en qué puerto. |
| `pnpm run pg:psql` | Abre `psql` contra el cluster. Acepta argumentos: `pnpm run pg:psql -- -d empires_test -c "select 1"`. |
| `pnpm run pg:destroy` | **Borra el cluster entero.** Los datos no son fuente de nada: se recrean con `init`. |

> **`db:*` y `pg:*` no son sinónimos.** Los scripts `db:up`, `db:psql`, `db:redis`, `db:reset` y
> `db:logs` operan sobre **`docker compose`**, así que en una máquina sin Docker no funcionan ninguno.
> Los `pg:*` operan el cluster propio de esta sección. Las excepciones son `db:migrate` y `db:version`,
> que invocan el binario de Go y sirven en ambos caminos.

El script vive en [`scripts/pg-local.mjs`](../../scripts/pg-local.mjs). En Windows localiza los
binarios en `C:\Program Files\PostgreSQL\<versión>`; en Linux y macOS los espera en el `PATH`. Con
`EO_PG_BIN` se puede forzar la ruta.

> Si prefieres usar un PostgreSQL que **ya administras** (y cuya contraseña de superusuario
> conoces), [`scripts/setup-local-db.sql`](../../scripts/setup-local-db.sql) crea en él el rol y las
> dos bases: `psql -U postgres -f scripts/setup-local-db.sql`. `psql` pedirá la contraseña de forma
> interactiva; no hay que escribirla en ningún archivo.

### 3-bis.2 Sin Redis: estado caliente en proceso

`EO_REDIS_URL` es **opcional en desarrollo**. Si se deja vacía, el servidor usa
`internal/persistence/memory` para las tres cosas que aporta Redis —presencia con expiración,
unicidad de tickets e idempotencia de comandos—, que son estado transitorio por definición
(ver [ADR-004](../decisions/ADR-004-redis-hot-state.md)).

Con **un solo proceso** las garantías son idénticas, y así lo verifican sus tests, que usan un reloj
falso y por tanto comprueban los TTL de forma instantánea y determinista.

Lo que NO da, y por eso la configuración lo **rechaza si `EO_ENV=production`**:

- No se comparte entre procesos: con dos instancias, un mismo ticket podría canjearse una vez en
  cada una y el anti-replay dejaría de serlo (`INV-SEC-003`).
- No sobrevive a un reinicio: al arrancar nadie está presente y todos los `requestId` vuelven a
  estar libres.

El servidor lo avisa en el arranque con un `WARN` deliberadamente ruidoso.

### 3-bis.3 El `.env` para este camino

```bash
EO_POSTGRES_URL=postgres://empires:empires_dev_password@localhost:5433/empires?sslmode=disable
EO_REDIS_URL=
```

El servidor **carga el `.env` por sí solo**: lo busca subiendo desde el directorio actual, así que
funciona igual desde la raíz del repositorio que desde `services/game-server`. Nunca pisa una
variable ya definida en el entorno —quien exporta algo a mano manda sobre el archivo— y su ausencia
no es un error, porque en producción no hay `.env`.

### 3-bis.4 Inspeccionar la base con pgAdmin

pgAdmin es un **cliente**, no un servidor: se conecta a un cluster ya existente y no cambia nada de
lo anterior. Suele venir incluido en el instalador oficial de PostgreSQL.

*Add New Server* → pestaña *Connection*:

| Campo | Valor |
|---|---|
| Host | `localhost` |
| Port | `5433` |
| Maintenance database | `empires` |
| Username | `empires` |
| Password | `empires_dev_password` |

Una advertencia al leer `units` desde pgAdmin: la columna `x`/`y` es la **posición consolidada**, no
necesariamente la actual. Mientras una unidad tiene un movimiento `ACTIVE`, su posición vigente se
deriva de la polilínea de `unit_movements` y solo se vuelca cada
`EO_PERSISTENCE_FLUSH_INTERVAL_TICKS`. No es un desfase accidental: es la estrategia descrita en
[persistence-strategy.md](../database/persistence-strategy.md).

### 3-bis.5 Aplicar el esquema

```bash
pnpm run db:migrate     # aplica las migraciones pendientes
pnpm run db:version     # muestra la versión aplicada y si el esquema quedó "dirty"
```

En desarrollo el servidor migra solo al arrancar (`EO_MIGRATE_ON_START`, por defecto activo fuera de
producción). En producción está desactivado a propósito: varias instancias levantándose a la vez
competirían por el mismo esquema, así que allí se ejecuta el binario `migrate` como paso previo del
despliegue. Ver [ADR-012](../decisions/ADR-012-database-migrations.md).

### 3-bis.6 Ejecutar los tests de integración por este camino

```bash
cd services/game-server
EO_INTEGRATION=1 \
EO_TEST_POSTGRES_URL="postgres://empires:empires_dev_password@localhost:5433/empires_test?sslmode=disable" \
go test -count=1 -tags=integration ./internal/persistence/postgres/...
```

Faltan aquí dos cosas: los tests de integración de **Redis** (`internal/persistence/redis`), que
necesitan un Redis real, y el **detector de carreras**, porque `-race` se apoya en cgo y aborta con
`-race requires cgo` si no hay un compilador de C en el `PATH` —el caso de Windows—. Ambas se
resuelven en §3-bis.7.

### 3-bis.7 Detector de carreras y Redis: desde WSL

Dos comprobaciones exigen Linux y no tienen equivalente en Windows: `-race`, que necesita un
compilador de C, y los tests de integración de Redis, que necesitan un Redis real. **WSL las cubre
sin Docker y sin permisos de administrador.** Es el camino verificado.

**Requisito:** una distribución instalada. `wsl -l -v` la lista.

**Paso 1 — compilador y Redis** (pide tu contraseña de la distro, no la de Windows):

```bash
wsl -d Ubuntu -- bash -c "sudo apt-get update && sudo apt-get install -y build-essential redis-server"
```

**Paso 2 — Go dentro de WSL.** El Go de Windows no sirve: son binarios de otro sistema operativo. Y
**no uses `apt install golang-go`**: Ubuntu 22.04 trae Go 1.18 y `go.mod` exige 1.25.11, así que
fallaría con un error sobre la directiva `go` que no sugiere en absoluto que el problema sea la
versión del paquete. El tarball oficial va a la carpeta personal y no necesita `sudo`:

```bash
wsl -d Ubuntu -- bash -lc 'cd "$HOME" && curl -sSL -o go.tgz https://go.dev/dl/go1.27.0.linux-amd64.tar.gz && mkdir -p golang && tar -C golang --strip-components=1 -xzf go.tgz && rm go.tgz && golang/bin/go version'
```

**Paso 3 — arrancar Redis.** Escucha en 6379 y no requiere privilegios; `--save ""` evita que escriba
volcados en disco, que para tests no aportan nada:

```bash
wsl -d Ubuntu -- bash -lc 'redis-server --daemonize yes --port 6379 --save "" --appendonly no; sleep 1; redis-cli ping'
```

**Paso 4 — la suite completa con `-race`,** que es exactamente lo que ejecuta el job `integration` de
la CI. Ajusta la ruta `/mnt/...` a donde tengas el repositorio:

```bash
wsl -d Ubuntu -- bash -lc 'export PATH="$HOME/golang/bin:$PATH"; cd /mnt/d/Desktop/dev/empires-online/services/game-server && EO_INTEGRATION=1 EO_TEST_POSTGRES_URL="postgres://empires:empires_dev_password@localhost:5433/empires_test?sslmode=disable" EO_TEST_REDIS_URL="redis://localhost:6379/1" go test -race -count=1 -tags=integration ./...'
```

**Por qué `localhost:5433` alcanza el PostgreSQL de Windows desde Linux:** porque la distro es
**WSL 1**, que comparte la pila de red con Windows. En **WSL 2** no es así —tiene su propio espacio
de red— y hay que apuntar a la IP del host de Windows, o activar el modo de red *mirrored* en
Windows 11. Comprueba cuál tienes con `wsl -l -v`: la columna `VERSION` lo dice. Confundir las dos
produce un `connection refused` que parece un problema de PostgreSQL y no lo es.

**Nota de versión:** aquí Redis es el que traiga la distro (6.0.16 en Ubuntu 22.04) y la CI usa
`redis:7-alpine`. Los comandos que ejercen estos tests son muy anteriores a ambas versiones, pero la
diferencia existe.

**Para usar ese Redis también en el servidor**, y no sólo en los tests, pon en el `.env`:

```
EO_REDIS_URL=redis://localhost:6379/0
```

Con eso el arranque registra `"msg":"estado caliente en Redis"` en lugar del aviso de estado en proceso.
Se puede confirmar que la rama está viva mirando las claves que escribe —presencia con TTL, `jti` de
tickets consumidos e idempotencia de comandos, que son los tres usos que ADR-004 le asigna—:

```bash
wsl -d Ubuntu -e bash -lc 'for k in $(redis-cli -n 0 --scan); do echo "$k ttl=$(redis-cli -n 0 ttl $k)"; done'
```

### 3-bis.8 Comprobar el montaje de extremo a extremo

Con el servidor arrancado, esto recorre el vertical slice completo y dice si la máquina quedó bien montada:

```bash
pnpm run smoke
```

Es la misma comprobación que cierra un despliegue ([deployment.md](./deployment.md) §6, Paso 5). Verifica
`/health` y `/ready`, da de alta un jugador, hace el handshake, recibe el snapshot, ordena un movimiento y
**cierra la conexión a mitad de trayecto para reconectar después y comprobar que la unidad llegó igualmente**.
Falla con código distinto de cero y explica en qué paso.

---

## 4. Infraestructura local: `docker-compose.yml`

Archivo en la raíz del repositorio. Define **exactamente dos servicios**: Postgres 16 y Redis 7. El Game
Server y el frontend se ejecutan **fuera** de Compose durante el desarrollo, para conservar rebuild rápido,
depurador y logs en la terminal.

```yaml
# docker-compose.yml (raíz del repositorio)
services:
  postgres:
    image: postgres:16-alpine
    container_name: empires-postgres
    restart: unless-stopped
    environment:
      POSTGRES_USER: empires
      POSTGRES_PASSWORD: empires_dev_password
      POSTGRES_DB: empires
      # Colación determinista: evita que el orden de texto dependa del locale del host.
      POSTGRES_INITDB_ARGS: '--encoding=UTF8 --locale=C'
    ports:
      - '5432:5432'
    volumes:
      - postgres_data:/var/lib/postgresql/data
    healthcheck:
      test: ['CMD-SHELL', 'pg_isready -U empires -d empires']
      interval: 5s
      timeout: 3s
      retries: 10
      start_period: 10s

  redis:
    image: redis:7-alpine
    container_name: empires-redis
    restart: unless-stopped
    command: ['redis-server', '--appendonly', 'yes', '--save', '']
    ports:
      - '6379:6379'
    volumes:
      - redis_data:/data
    healthcheck:
      test: ['CMD', 'redis-cli', 'ping']
      interval: 5s
      timeout: 3s
      retries: 10
      start_period: 5s

volumes:
  postgres_data:
    name: empires_postgres_data
  redis_data:
    name: empires_redis_data
```

Notas de diseño, no cosméticas:

- **Nombre de base y credenciales:** usuario `empires`, contraseña `empires_dev_password`, base **`empires`**
  (no `empires_online`). Es lo que espera `EO_POSTGRES_URL` en `.env.example` y lo que usan `db:psql` y los
  healthchecks. Son **valores de desarrollo local**, no constantes del canon: en producción las credenciales
  vienen del gestor de secretos, ver [configuration.md](./configuration.md#6-manejo-de-secretos).
- **Redis arranca con `--appendonly yes --save ''`**, es decir con AOF y sin snapshots RDB. El AOF es una
  comodidad de desarrollo, **no una garantía de diseño**: Redis es estado *hot/transient* y **nunca** fuente
  de verdad. Si algo se rompe al vaciar Redis, es un bug de diseño, no un problema de configuración — por eso
  merece la pena provocarlo a mano (§6.2). Ver
  [backups.md](./backups.md#2-por-qué-redis-no-se-respalda) y
  [disaster-recovery.md](./disaster-recovery.md#e5-redis-caído-o-vaciado).
- **Volúmenes nombrados** (`empires_postgres_data`, `empires_redis_data`), no bind mounts: evitan los
  problemas de permisos y de rendimiento de NTFS montado en el contenedor.
- **Healthchecks** en ambos servicios, para poder saber cuándo la infraestructura está realmente lista y no
  solo arrancada. `db:up` **no** usa `--wait`: si quieres bloquear hasta `healthy`, añádelo tú (§5.3).

---

## 5. Puesta en marcha paso a paso

### 5.1 Clonar e instalar dependencias

```powershell
git clone <url-del-repo> D:\Desktop\dev\empires-online
cd D:\Desktop\dev\empires-online
pnpm install
```

`pnpm install` resuelve el workspace (`packages/protocol` hoy; `apps/web` cuando exista). Las dependencias de
Go se resuelven aparte, **desde `services/game-server`**:

```powershell
cd services\game-server
go mod download
cd ..\..
```

### 5.2 Crear el `.env` a partir de `.env.example`

`.env.example` está versionado y **no contiene secretos reales**. `.env` está en `.gitignore` y nunca se
sube. Contenido de `.env.example`:

```dotenv
# Empires Online — plantilla de configuración.
#
# Copia este archivo a `.env` y ajusta los valores. `.env` está en .gitignore:
# NUNCA se versionan secretos. Referencia completa: docs/operations/configuration.md
#
#   Windows PowerShell:  Copy-Item .env.example .env
#   Bash:                cp .env.example .env

# ─────────────────────────────────────────────────────────────
# Entorno y observabilidad
# ─────────────────────────────────────────────────────────────
EO_ENV=development
EO_LOG_LEVEL=debug
EO_HTTP_ADDR=:8080
EO_METRICS_ADDR=:9090

# ─────────────────────────────────────────────────────────────
# Infraestructura (SECRETOS en producción)
# ─────────────────────────────────────────────────────────────
EO_POSTGRES_URL=postgres://empires:empires_dev_password@localhost:5432/empires?sslmode=disable
EO_REDIS_URL=redis://localhost:6379/0

# Secreto compartido entre Next.js (emisor del game ticket) y el Game Server (verificador).
# Genera uno propio con: openssl rand -base64 48
EO_AUTH_JWT_SECRET=dev-only-insecure-secret-change-me-before-any-deployment

# ─────────────────────────────────────────────────────────────
# Simulación
# ─────────────────────────────────────────────────────────────
EO_TICK_RATE_HZ=10
EO_WORLD_WIDTH=512
EO_WORLD_HEIGHT=512
EO_WORLD_SEED=20260909
EO_CHUNK_SIZE=32
EO_INTEREST_RADIUS_CHUNKS=2

# ─────────────────────────────────────────────────────────────
# Presencia y protección offline (valores de gameplay: nunca hardcodear)
# ─────────────────────────────────────────────────────────────
EO_PRESENCE_TTL_SECONDS=30
EO_PRESENCE_HEARTBEAT_SECONDS=10
EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS=300

# ─────────────────────────────────────────────────────────────
# Persistencia
# ─────────────────────────────────────────────────────────────
EO_PERSISTENCE_FLUSH_INTERVAL_TICKS=50

# ─────────────────────────────────────────────────────────────
# Pathfinding
# ─────────────────────────────────────────────────────────────
EO_PATHFINDING_MAX_NODES=20000
EO_PATHFINDING_MAX_DISTANCE=256

# ─────────────────────────────────────────────────────────────
# WebSocket
# ─────────────────────────────────────────────────────────────
EO_WS_MAX_MESSAGE_BYTES=16384
EO_WS_RATE_LIMIT_PER_SECOND=20
EO_WS_RATE_LIMIT_BURST=40

# ─────────────────────────────────────────────────────────────
# Frontend (Next.js) — las variables NEXT_PUBLIC_* son visibles en el navegador.
# Jamás pongas aquí credenciales de PostgreSQL, Redis ni el secreto JWT.
# ─────────────────────────────────────────────────────────────
NEXT_PUBLIC_GAME_SERVER_WS_URL=ws://localhost:8080/ws
```

`.env.example` **no incluye** `EO_WS_OUTBOUND_QUEUE_SIZE`: existe, la lee `internal/config` y su valor por
defecto es 256 (rango 8–65536). Solo hace falta declararla si quieres apartarte del default. Ver
[configuration.md](./configuration.md#38-websocket).

Derivación del `.env` real:

```powershell
Copy-Item .env.example .env
# Generar un secreto local aleatorio de 32 bytes en base64 (el mínimo son 32 caracteres):
$bytes = New-Object byte[] 32
[Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
$secret = [Convert]::ToBase64String($bytes)
(Get-Content .env) -replace '^EO_AUTH_JWT_SECRET=.*', "EO_AUTH_JWT_SECRET=$secret" | Set-Content .env
```

```bash
# Linux/macOS
cp .env.example .env
sed -i "s|^EO_AUTH_JWT_SECRET=.*|EO_AUTH_JWT_SECRET=$(openssl rand -base64 48)|" .env
```

El secreto de la plantilla contiene literalmente `dev-only`, y esa cadena es **rechazada al arrancar** si
`EO_ENV=production`. En local funciona, pero regenerarlo cuesta dos líneas y evita el hábito.

El significado, rango y criticidad de cada variable está en [configuration.md](./configuration.md).

### 5.3 Levantar la infraestructura

```powershell
pnpm run db:up
```

Equivalente directo:

```powershell
docker compose up -d postgres redis
```

`db:up` **no** espera a los healthchecks. Si quieres bloquear hasta que ambos estén `healthy` —lo sensato
antes de lanzar tests—, añade `--wait` a mano:

```powershell
docker compose up -d --wait postgres redis
```

Verificación:

```powershell
docker compose ps
# postgres  empires-postgres  running (healthy)  0.0.0.0:5432->5432/tcp
# redis     empires-redis     running (healthy)  0.0.0.0:6379->6379/tcp

pnpm run db:logs      # docker compose logs -f postgres redis
```

Tumbar la infraestructura:

```powershell
pnpm run db:down    # docker compose down                                          (conserva volúmenes)
pnpm run db:reset   # docker compose down -v && docker compose up -d postgres redis (BORRA volúmenes)
```

`db:reset` es destructivo y solo tiene sentido en local. En cualquier otro entorno, ver
[backups.md](./backups.md).

### 5.4 Migraciones: las aplica el propio servidor al arrancar

**No hay ningún script `db:migrate`, y no hace falta.** Las migraciones viven en
`services/game-server/migrations/` con el patrón `NNNNNN_nombre.up.sql` / `NNNNNN_nombre.down.sql`, están
**embebidas en el binario** con `go:embed`, y el servidor las aplica automáticamente al arrancar
(`postgres.Migrate`, sobre **golang-migrate**, que reescribe la URL al esquema `pgx5`). El estado aplicado se
registra en la tabla `schema_migrations`, que golang-migrate mantiene con dos columnas: `version` y `dirty`.

Migraciones existentes hoy:

| Archivo | Contenido |
|---|---|
| `000001_initial_schema.up.sql` / `.down.sql` | Esquema completo: `civilizations`, `factions`, `eras`, `players`, `world_state`, `world_chunks`, `cities`, `units`, `unit_movements`, `sessions`, `idempotency_keys`, `territories`, `territory_control`, `safe_zones`, `treaties`, `garrisons`, `world_events` |
| `000002_seed_catalogs.up.sql` / `.down.sql` | Semillas de catálogo: civilizaciones ROMAN/BYZANTINE/PERSIAN/NORSE, facciones ORDER/CHAOS/NEUTRAL, eras STONE_AGE(20)/BRONZE_AGE(50)/IRON_AGE(100)/CASTLE_AGE(150) |

Consultar el estado desde el contenedor:

```powershell
docker compose exec postgres psql -U empires -d empires -c "SELECT version, dirty FROM schema_migrations;"
```

Si una migración falla a medias, golang-migrate marca el esquema como **`dirty`** y el servidor **se niega a
arrancar**, con un mensaje que nombra la versión afectada. Es deliberado: continuar sería adivinar. Ver §10.3.
Detalles del esquema en [../database/schema.md](../database/schema.md) y del diseño de migraciones en
[../database/migrations.md](../database/migrations.md).

### 5.5 Datos iniciales: catálogos y alta de jugador

**Tampoco existe un script `db:seed`.** Los datos iniciales llegan por dos vías distintas:

1. **Catálogos** (`civilizations`, `factions`, `eras`): los siembra la migración `000002_seed_catalogs`, así
   que están en cuanto el servidor arranca por primera vez.
2. **Mundo** (`world_state` con su `epoch_ms`, y los 256 chunks de `world_chunks` — 512×512 tiles con chunks
   de 32×32): los crea el servidor la primera vez que arranca contra una base vacía.
3. **Jugadores y ciudades**: se crean dándose de alta por la API, no con una semilla:

   ```powershell
   curl.exe -X POST http://localhost:8080/api/auth/register `
     -H "Content-Type: application/json" `
     -d '{\"username\":\"tester\",\"password\":\"un-password-largo\"}'
   ```

   El alta ejecuta primero una transacción atómica en PostgreSQL (jugador + ciudad + 3 `VILLAGER`) y solo
   después incorpora al jugador al mundo en RAM. El emplazamiento de la ciudad es **determinista**: búsqueda
   en espiral desde una semilla derivada del nombre de usuario, exigiendo un entorno despejado de radio 3 y
   una separación mínima de 24 tiles entre centros de ciudad; la muralla es un rectángulo 3×3 bloqueado
   alrededor del centro y los 3 aldeanos nacen a radio 2. `username` debe casar con
   `^[A-Za-z0-9_-]{3,24}$`.

   > `/api/auth/register` y `/api/auth/login` los sirve **hoy el propio Game Server**
   > (`internal/httpapi`, con bcrypt). Es explícitamente provisional: la arquitectura objetivo
   > ([ADR-010](../decisions/ADR-010-authentication-game-ticket.md)) los traslada a Next.js.

**El terreno se regenera desde la semilla en cada arranque.** `world_chunks` guarda una copia para auditoría
y para permitir mapas editados en el futuro, pero **no es la fuente primaria**: regenerar es más barato que
leer 256 filas y garantiza que semilla y mapa nunca divergen. La consecuencia práctica es buena:
`db:reset` + arrancar el servidor reconstruye **el mismo mundo byte a byte** mientras `EO_WORLD_SEED` no
cambie. Eso es una propiedad, no una casualidad: es lo que permite que los tests de simulación sean
reproducibles.

### 5.6 Arrancar el Game Server

```powershell
pnpm run server:run
```

Equivalente:

```powershell
cd services\game-server
go run ./cmd/server
```

No hay *hot reload*: tras cambiar código Go hay que parar el proceso y volver a lanzarlo.

Salida esperada (JSON estructurado, `log/slog`), con el loop a 10 Hz y los endpoints operativos:

```powershell
curl.exe http://localhost:8080/health   # 200 siempre que el proceso viva
curl.exe http://localhost:8080/ready    # 200 solo si Postgres + Redis + loop están OK
curl.exe http://localhost:9090/metrics  # exposición Prometheus
```

La semántica exacta de `/health` y `/ready` está en [monitoring.md](./monitoring.md#3-endpoints-de-salud).

### 5.7 Arrancar el frontend — *pendiente del milestone de frontend*

**`apps/web/` ya existe en el repositorio**, así que `pnpm run web:dev` sí tiene algo que
arrancar. Lo que sigue describe el objetivo, para que el `.env.local` se cree bien desde el primer día.

```powershell
pnpm run web:dev     # cuando exista apps/web
```

Con `apps/web/.env.local`:

```dotenv
NEXT_PUBLIC_GAME_SERVER_WS_URL=ws://localhost:8080/ws
EO_AUTH_JWT_SECRET=<el mismo valor que el .env del Game Server>
```

El frontend quedará en `http://localhost:3000`. La API route de Next emitirá el *game ticket* (JWT HS256,
TTL 60 s, `aud: "game-server"`) firmado con `EO_AUTH_JWT_SECRET`; **si el secreto del frontend y el del Game
Server no coinciden, el handshake se cierra con `4401`**. Es el error de configuración local más frecuente.

Mientras tanto, el flujo completo se puede ejercitar sin frontend: `POST /api/auth/register` o
`POST /api/auth/login` devuelven el ticket, y con él se abre el WebSocket contra `ws://localhost:8080/ws`
enviando `session.hello` como **primer** mensaje.

---

## 6. Conectarse a Postgres y a Redis sin `psql` ni `redis-cli`

Las imágenes `postgres:16-alpine` y `redis:7-alpine` traen los clientes dentro, así que por el camino
de Docker no hace falta instalarlos en el host.

> **Por el camino de §3-bis los comandos son otros**, y esta sección entera no aplica:
>
> | | Docker (esta sección) | Sin Docker (§3-bis) |
> |---|---|---|
> | Postgres | `docker compose exec postgres psql …` | `pnpm run pg:psql` — el `psql` del host, en `C:\Program Files\PostgreSQL\16\bin` |
> | Redis | `docker compose exec redis redis-cli` | `wsl -d Ubuntu -- redis-cli` |
>
> `pnpm run pg:psql` acepta argumentos igual que `psql`:
> `pnpm run pg:psql -- -d empires_test -c "SELECT version, dirty FROM schema_migrations;"`

### 6.1 Postgres

```powershell
# Shell interactiva (atajo: pnpm run db:psql)
docker compose exec postgres psql -U empires -d empires

# Consulta puntual (no interactiva)
docker compose exec postgres psql -U empires -d empires -c "SELECT version, dirty FROM schema_migrations;"

# Inspeccionar movimientos activos
docker compose exec postgres psql -U empires -d empires -c "SELECT id, unit_id, status, start_time_ms, arrival_time_ms FROM unit_movements WHERE status = 'ACTIVE';"

# Volcado lógico a un archivo del host (no hay script: se invoca a mano)
docker compose exec -T postgres pg_dump -U empires -d empires > .\backup-local.sql
```

Atajo disponible: `pnpm run db:psql`. Recuerda que la base se llama **`empires`**, no `empires_online`.

### 6.2 Redis

```powershell
# Shell interactiva (atajo: pnpm run db:redis)
docker compose exec redis redis-cli

# Claves de presencia, idempotencia y anti-replay del ticket
docker compose exec redis redis-cli --scan --pattern "presence:player:*"
docker compose exec redis redis-cli --scan --pattern "idem:*"
docker compose exec redis redis-cli --scan --pattern "ticket:jti:*"
docker compose exec redis redis-cli TTL "presence:player:<playerId>"

# Vaciar Redis a propósito, para verificar que el sistema degrada y no se rompe
docker compose exec redis redis-cli FLUSHALL
```

Atajo: `pnpm run db:redis`.

> El ejercicio de `FLUSHALL` con jugadores conectados debería hacerse al menos una vez por cada
> desarrollador. El resultado esperado está descrito en
> [disaster-recovery.md](./disaster-recovery.md#e5-redis-caído-o-vaciado): degradación temporal, cero pérdida
> de estado durable. En particular, **vaciar Redis no expulsa a nadie ni dispara `OFFLINE_PENDING`**: esa
> transición la decide el game loop en RAM comparando contra `DisconnectGrace` (= `EO_PRESENCE_TTL_SECONDS`,
> 30 s), no leyendo la expiración de la clave de Redis.

---

## 7. Flujo de trabajo diario

```powershell
# 1. Arrancar Docker Desktop si hace falta y comprobarlo (§3)
docker info

# 2. Arrancar infraestructura (idempotente: si ya corre, no hace nada)
pnpm run db:up

# 3. Sincronizar. Las migraciones nuevas del equipo se aplican solas
#    la próxima vez que arranques el servidor.
git pull
pnpm install

# 4. Terminal de trabajo
pnpm run server:run

# 5. Antes de abrir PR: la verificación completa en un solo comando
pnpm run verify
```

`pnpm run verify` encadena exactamente esto, y en este orden:

```
protocol:build  →  docs:check  →  typecheck  →  test  →  server:fmt:check  →  server:vet  →  server:test
```

Es decir: regenera el JSON Schema, verifica la integridad de `docs/` (enlaces relativos, IDs `INV-*`
definidos y nombres de ADR existentes, con `node scripts/check-docs.mjs`), comprueba tipos de TypeScript,
ejecuta los tests de Vitest, comprueba el formato de Go con `gofmt -l .`, pasa `go vet ./...` y ejecuta
`go test ./...`. Si `verify` está en verde, el PR tiene la base cubierta — **salvo los tests de integración**,
que `verify` no ejecuta (§9).

Si tocas `packages/protocol`, **regenera el JSON Schema antes de compilar Go**: los esquemas Zod son la
fuente de verdad, el build exporta `packages/protocol/schema/v1/*.json` **y su espejo en
`services/game-server/internal/protocol/schema/v1/`**, que el Game Server embebe con `go:embed`. Un espejo
desactualizado hace fallar los contract tests con un diff confuso.

```powershell
pnpm run protocol:build     # exporta y actualiza el espejo
pnpm run protocol:check     # falla si el espejo ha derivado, sin escribir nada (apto para CI)
```

Orden ritual tras cambiar el protocolo: `protocol:build → protocol:test → server:test → server:run`.

---

## 8. Tabla de scripts pnpm

Contrato de tareas del monorepo, tal y como está hoy en `package.json`. Cualquier tarea que un documento
mencione debe existir aquí; si no está en esta tabla, no existe.

| Script | Qué hace |
|---|---|
| `pnpm run db:up` | `docker compose up -d postgres redis` |
| `pnpm run db:down` | `docker compose down` (conserva volúmenes) |
| `pnpm run db:reset` | `docker compose down -v && docker compose up -d postgres redis` (**destruye** volúmenes) |
| `pnpm run db:logs` | `docker compose logs -f postgres redis` |
| `pnpm run db:psql` | `docker compose exec postgres psql -U empires -d empires` |
| `pnpm run db:redis` | `docker compose exec redis redis-cli` |
| `pnpm run protocol:build` | Compila el paquete y exporta Zod → JSON Schema (`packages/protocol/schema/v1/` + espejo en Go) |
| `pnpm run protocol:test` | Tests de Vitest del paquete de protocolo |
| `pnpm run protocol:check` | Comprueba que el JSON Schema exportado no ha derivado; no escribe |
| `pnpm run server:tidy` | `go mod tidy` en `services/game-server` |
| `pnpm run server:build` | `go build -o bin/empires-server ./cmd/server` |
| `pnpm run server:run` | `go run ./cmd/server` |
| `pnpm run server:test` | `go test ./...` |
| `pnpm run server:test:integration` | `go test -tags=integration ./...` (requiere Docker, §9) |
| `pnpm run server:vet` | `go vet ./...` |
| `pnpm run server:fmt` | `gofmt -l -w .` (reescribe) |
| `pnpm run server:fmt:check` | `gofmt -l .` (solo lista; apto para CI) |
| `pnpm run web:dev` / `web:build` | Next.js en `apps/web` — desarrollo en `http://localhost:3000` / build de producción |
| `pnpm run docs:check` | `node scripts/check-docs.mjs`: enlaces relativos rotos, IDs `INV-*` citados pero no definidos en `docs/invariants/`, y nombres de ADR inexistentes |
| `pnpm run lint` / `typecheck` / `test` | Recursivos sobre los paquetes del workspace (`pnpm -r run …`) |
| `pnpm run verify` | `protocol:build → docs:check → typecheck → test → server:fmt:check → server:vet → server:test` |

Todos los scripts `server:*` hacen `cd services/game-server` por ti: es ahí donde vive el `go.mod`.

---

## 9. Ejecutar los tests

Los niveles y su intención están definidos en [../testing/strategy.md](../testing/strategy.md). Aquí solo la
mecánica local. **No existen scripts `test:unit`, `test:contract`, `test:simulation` ni `test:recovery`**:
todo el conjunto de Go va por `server:test`, y lo separan los paquetes, no los comandos.

| Qué | Comando | Requiere Docker | Notas |
|---|---|---|---|
| Protocolo (TypeScript) | `pnpm run protocol:test` | No | 18 tests de Vitest: envelopes, versión no soportada, coordenadas no enteras, rechazo de campos extra, catálogo de errores, JSON Schema válido y sin deriva. **En verde.** |
| Deriva del JSON Schema | `pnpm run protocol:check` | No | Falla si el espejo embebido en Go difiere de lo que exportan los esquemas Zod. |
| Go: dominio, mundo, pathfinding, config, auth, protocolo y simulación | `pnpm run server:test` | No | `go test ./...`. Dominio puro con `FakeClock` y `RandomSource` inyectados; incluye los contract tests Go ↔ TypeScript, el *vertical slice* de simulación y los 3 tests de recuperación. **En verde.** |
| Análisis estático | `pnpm run server:vet` | No | `go vet ./...`. **En verde.** |
| Integración (PostgreSQL + Redis reales) | `pnpm run server:test:integration` | **Sí** | `go test -tags=integration ./...`, y además exige `EO_INTEGRATION=1`. Ver aviso más abajo. |
| Carga (k6) | — | — | **Fuera de MVP.** |

Los tests de integración tienen **dos** puertas, y hacen falta las dos: la etiqueta de compilación
`integration` (la pone el script) y la variable `EO_INTEGRATION=1`.

```powershell
$env:EO_INTEGRATION = "1"; pnpm run server:test:integration
```

```bash
EO_INTEGRATION=1 pnpm run server:test:integration
```

Sin la variable, esos tests **se saltan** (`t.Skip`) en lugar de fallar. Es deliberado: permite
`pnpm run server:test` en una máquina sin daemon, y a la vez CI los ejecuta siempre porque allí la variable
está puesta. Un test de integración que "pasa" en verde sin Docker es un test que no se ejecutó: comprueba
la salida por `--- SKIP`.

> **Estado real:** los tests de integración están **diseñados pero NO ejecutados** en esta máquina, porque el
> daemon de Docker no arrancó (§1, §3). No están en rojo ni en verde: están sin correr, y así hay que
> contarlo hasta que alguien los ejecute con el daemon vivo.

---

## 10. Solución de problemas frecuentes

### 10.1 Puerto ocupado (5432, 6379, 8080, 9090, 3000)

Síntoma: `Ports are not available: ... bind: An attempt was made to access a socket in a way forbidden`,
o el Game Server muere al arrancar con `address already in use`.

```powershell
# Quién ocupa el puerto
netstat -ano | Select-String ":5432"
Get-Process -Id <PID>
```

```bash
lsof -i :5432        # Linux/macOS
```

Causas típicas y remedio:

- **Un Postgres instalado en el host** escuchando en 5432. Detén el servicio, o cambia el mapeo del
  contenedor a `"5433:5432"` y actualiza `EO_POSTGRES_URL` al puerto 5433.
- **Un `docker compose` anterior de otro proyecto**: `docker ps` y `docker stop <container>`.
- **Windows reservó el rango dinámico** (`netsh interface ipv4 show excludedportrange protocol=tcp`).
  Si el puerto cae en un rango excluido, cámbialo; no intentes forzarlo.

Nunca resuelvas esto matando procesos a ciegas: `Stop-Process` sobre el PID equivocado puede tirar el propio
Docker Desktop.

### 10.2 El daemon de Docker está apagado

Síntoma: cualquier comando `docker compose …` falla con `Cannot connect to the Docker daemon`, o
`pnpm run server:test:integration` falla al conectar a Postgres.

Diagnóstico y arreglo: §3. Recuerda que `docker --version` responde igualmente con el daemon apagado; usa
`docker info`. Tras arrancar Docker Desktop, vuelve a ejecutar `pnpm run db:up` (es idempotente).

### 10.3 Migración fallida: el esquema queda `dirty`

Síntoma: el servidor **se niega a arrancar** con un mensaje del tipo
`el esquema está marcado como dirty en la versión N: requiere intervención manual`.

Es golang-migrate diciendo que una migración anterior falló a medias. Continuar automáticamente sería
adivinar, así que el arranque se aborta a propósito.

1. **Lee el error real** de la ejecución que falló. Casi siempre es un `CHECK` violado por datos de semilla,
   o un `NOT NULL` añadido sobre una columna con filas existentes.
2. Inspecciona el estado:

   ```powershell
   docker compose exec postgres psql -U empires -d empires -c "SELECT version, dirty FROM schema_migrations;"
   ```

3. Corrige el `.up.sql` causante.
4. **En local, la salida sin drama es reconstruir:** `pnpm run db:reset` y volver a arrancar el servidor, que
   reaplicará las migraciones desde cero. El mundo se regenera idéntico desde `EO_WORLD_SEED`, así que no
   pierdes nada irremplazable.

En cualquier entorno **que no sea local**, esta ruta no existe: un esquema `dirty` es un incidente con
persona al mando. Ver [disaster-recovery.md](./disaster-recovery.md#e7-despliegue-defectuoso), y el
procedimiento de [backups.md](./backups.md#7-backup-lógico-previo-a-migración-destructiva) y
[deployment.md](./deployment.md#7-rollback).

### 10.4 WSS en local: usa `ws://`, sin TLS

En producción el transporte es **WSS** (canon §13) y lo termina el reverse proxy
(ver [deployment.md](./deployment.md#4-reverse-proxy-y-terminación-tls)). En local **no hay proxy ni
certificado**: el Game Server escucha HTTP plano en `EO_HTTP_ADDR=:8080` y el cliente debe conectar a
`ws://localhost:8080/ws`.

Errores típicos y su causa:

| Síntoma en el navegador | Causa | Remedio |
|---|---|---|
| `SecurityError: An insecure WebSocket connection may not be initiated from a page loaded over HTTPS` | La página va por `https://` y el WS por `ws://` | Sirve el frontend en `http://localhost:3000` (por defecto en `web:dev`) |
| `WebSocket connection failed` inmediato | `NEXT_PUBLIC_GAME_SERVER_WS_URL` apunta a `wss://localhost:8080/ws` | Corrige a `ws://localhost:8080/ws` |
| Conexión abierta y cierre `4401` | `EO_AUTH_JWT_SECRET` distinto entre Next y Game Server, o ticket ya expirado (TTL 60 s) | Iguala el secreto; no reutilices tickets |
| Cierre `4408` a los 5 s | El cliente no envió `session.hello` a tiempo | Revisa el orden: `session.hello` debe ser el **primer** mensaje |
| Cierre `4429` | Superaste 20 msg/s con burst 40 | Es el rate limit funcionando; no lo subas para "arreglarlo" |

`localhost` se considera *secure context*, así que no hace falta certificado en desarrollo. **No generes
certificados autofirmados para local**: añade fricción y esconde justamente los problemas de configuración
del proxy que quieres detectar en staging.

### 10.5 El servidor arranca pero `/ready` devuelve 503

`/health` responde 200 (el proceso vive) pero `/ready` no. Por definición, `/ready` comprueba Postgres,
Redis y que el game loop siga latiendo (umbral de 5 s de silencio). El cuerpo de la respuesta trae un mapa
`checks` con una entrada por dependencia — `postgres`, `redis` y `game_loop` — que dice exactamente cuál
falla. Léelo antes de adivinar:

```powershell
curl.exe http://localhost:8080/ready
docker compose ps                       # ¿healthy ambos?
curl.exe http://localhost:9090/metrics | Select-String "eo_game_tick"
```

Si `eo_game_tick_duration_seconds_count` no aumenta entre dos lecturas, el loop está bloqueado: casi siempre
por I/O síncrono introducido por error dentro del tick, algo que el canon prohíbe explícitamente
(ver [../architecture/game-loop.md](../architecture/game-loop.md)).

### 10.6 Go no encuentra el módulo / build lento la primera vez

`go mod download` en frío descarga toda la caché de módulos: puede tardar varios minutos y no es un cuelgue.
Si aparece `go: cannot find main module`, estás fuera de `services/game-server`; los scripts de pnpm ya
hacen `cd` por ti, así que prefiérelos a invocar `go` a mano.

---

## 11. Qué NO existe en el entorno local

Marcado explícitamente para que nadie lo busque:

- **`apps/web/`**: el frontend Next.js **aún no existe** en el repositorio. `web:dev` y `web:build` están
  declarados en `package.json` pero todavía no tienen paquete que ejecutar.
- **Scripts `db:migrate*`, `db:seed`, `db:dump`, `db:nuke`, `server:dev`, `redis:cli`, `format`, `test:unit`,
  `test:integration`, `test:contract`, `test:simulation`, `test:recovery`**: no existen. Los equivalentes
  reales están en la tabla de §8.
- **Subcomandos del binario** (`migrate up`, `migrate status`, `healthcheck`): no existen. El binario hace
  una sola cosa: arrancar el servidor, aplicando antes las migraciones embebidas.
- **Combate, economía, tecnologías, comercio, clanes, chat, ranking**: fuera del primer vertical slice.
- **Tests de carga (k6)**: fuera de MVP.
- **Hot reload de configuración**: cambiar una variable `EO_` exige reiniciar el proceso.
- **Hot reload de código Go**: tampoco; `server:run` no observa cambios.
- **TLS en local**: intencionalmente ausente (§10.4).
- **Makefile**: no existe y no se creará; `make` no está instalado.

---

## Referencias

- [configuration.md](./configuration.md) — referencia completa de variables `EO_`.
- [deployment.md](./deployment.md) — despliegue de frontend y Game Server.
- [monitoring.md](./monitoring.md) — métricas, logs, `/health` y `/ready`.
- [backups.md](./backups.md) · [disaster-recovery.md](./disaster-recovery.md)
- [../architecture/game-loop.md](../architecture/game-loop.md) — fases del tick.
- [../database/schema.md](../database/schema.md) — esquema real de la migración `000001`.
- [../database/migrations.md](../database/migrations.md) — diseño y ciclo de vida de las migraciones.
- [../testing/strategy.md](../testing/strategy.md) — niveles de test y Definition of Done.
- [../decisions/ADR-012-database-migrations.md](../decisions/ADR-012-database-migrations.md) — migraciones SQL embebidas.

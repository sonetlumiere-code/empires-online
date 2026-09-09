# Despliegue

Procedimiento completo de despliegue de Empires Online: frontend Next.js en Vercel, Game Server en VPS dedicado, Postgres y Redis gestionados, reverse proxy con terminación TLS y upgrade a WSS, migraciones, verificación, rollback y checklist de seguridad.

> Estado: **diseño objetivo**. El proyecto es greenfield; no hay ningún entorno desplegado todavía. Este
> documento es la especificación que debe cumplirse en el primer despliegue, no la descripción de uno
> existente.

---

## 1. Topología

```
                    ┌──────────────────────────────────────────────┐
   Navegador ──────►│ Vercel  ·  apps/web (Next.js 15 + PixiJS 8)  │
   (HTTPS)          │  - páginas y assets                          │
                    │  - API route: emite game ticket (JWT HS256)  │
                    └──────────────────┬───────────────────────────┘
                                       │  ticket (TTL 60 s)
                                       ▼
   Navegador ─── WSS ──────► ┌─────────────────────────────────────┐
                             │ VPS dedicado                        │
                             │  ┌──────────────────────────────┐   │
                             │  │ Caddy · TLS + upgrade WSS    │   │
                             │  └──────────┬───────────────────┘   │
                             │             │ HTTP :8080 (loopback) │
                             │  ┌──────────▼───────────────────┐   │
                             │  │ eo-game-server (contenedor)  │   │
                             │  │  /ws  /health  /ready        │   │
                             │  │  :9090 /metrics (privado)    │   │
                             │  └──────────┬───────────────────┘   │
                             └─────────────┼───────────────────────┘
                                           │ red privada + TLS
                          ┌────────────────┴────────────────┐
                          ▼                                 ▼
                ┌───────────────────┐            ┌────────────────────┐
                │ PostgreSQL 16     │            │ Redis 7            │
                │ gestionado        │            │ gestionado         │
                │ durable SoT       │            │ hot/transient      │
                └───────────────────┘            └────────────────────┘
```

Decisiones estructurales:

- **Un único proceso de Game Server.** El estado autoritativo vive en la RAM de ese proceso y el loop es
  determinista y de hilo único por fase. Escalado horizontal del Game Server (sharding por región del
  mundo): **fuera de MVP**.
- **Frontend y Game Server despliegan por separado.** El frontend es *stateless* y se despliega en cualquier
  momento; el Game Server tiene ventana de indisponibilidad (§8) y se despliega con procedimiento.
- **`/metrics` nunca es público.** Escucha en `EO_METRICS_ADDR` y solo es alcanzable desde la red de
  monitorización.

---

## 2. Frontend: Next.js en Vercel

### 2.1 Variables públicas frente a privadas

La distinción es de seguridad, no de estilo. Todo lo que lleva prefijo `NEXT_PUBLIC_` **se incrusta en el
bundle JavaScript** que se descarga el navegador: es público de forma irreversible.

| Variable | Prefijo | Ámbito | Contenido |
|---|---|---|---|
| `NEXT_PUBLIC_GAME_SERVER_WS_URL` | **público** | Navegador y servidor | `wss://game.<dominio>/ws` |
| `EO_AUTH_JWT_SECRET` | **privado** | Solo runtime del servidor de Next | Clave HS256 compartida con el Game Server |

`NEXT_PUBLIC_GAME_SERVER_WS_URL` es una variable **del frontend**, no del catálogo `EO_` del Game Server:
`internal/config` no la lee y no aparece en la referencia de [configuration.md §3](./configuration.md#3-referencia-de-variables).
Su nombre es el que figura en `.env.example` de la raíz.

La URL del WebSocket **debe** ser pública: el navegador necesita saber a dónde conectar. No hay nada
sensible en ella, y ocultarla no aportaría seguridad alguna — el endpoint es público por diseño y su
protección real es el handshake autenticado.

`EO_AUTH_JWT_SECRET` **jamás** lleva prefijo `NEXT_PUBLIC_`. Se usa exclusivamente dentro de la API route
que emite el ticket. Un secreto expuesto en el bundle permitiría a cualquiera firmar un ticket con el `sub`
de otro jugador y suplantarlo por completo. Ver
[configuration.md](./configuration.md#6-manejo-de-secretos).

Configuración en Vercel: *Project → Settings → Environment Variables*, con los tres ámbitos separados.

| Ámbito | `NEXT_PUBLIC_GAME_SERVER_WS_URL` | `EO_AUTH_JWT_SECRET` |
|---|---|---|
| Production | `wss://game.<dominio>/ws` | secreto de producción |
| Preview | `wss://game-staging.<dominio>/ws` | secreto de staging |
| Development | `ws://localhost:8080/ws` | secreto local |

### 2.2 Previews

Cada PR genera un despliegue *preview* con URL propia. Reglas:

- Un preview **nunca** apunta al Game Server de producción. Apunta a staging, con su propio secreto, su
  propia base y su propio mundo. Un preview que escribiera en el mundo de producción sería una vía de
  corrupción de estado durable desde una rama sin revisar.
- El Game Server de staging debe aceptar el origen del preview en su comprobación de `Origin` durante el
  upgrade WebSocket. Como las URLs de preview son dinámicas, staging admite el patrón de dominio de preview;
  producción admite **exclusivamente** el dominio del frontend.
- Los previews son públicos por defecto en Vercel: activa la protección de despliegue si el contenido no
  debe ser accesible.

### 2.3 Build y despliegue

> **`apps/web/` ya existe en el repositorio** (Next.js 15 + React 19 + PixiJS 8, 58 tests en verde y `next build` correcto). Los scripts `web:dev` y `web:build` están declarados
> en `package.json`, pero no hay paquete que construir. Todo el §2 describe el despliegue objetivo del
> frontend, no uno existente.

- Build command: `pnpm run web:build` desde la raíz del monorepo; `apps/web` como directorio de la app.
- El paquete `@empires-online/protocol` se construye como dependencia del workspace: los esquemas Zod deben
  compilar antes que la app.
- El despliegue del frontend es **atómico y reversible**: Vercel conserva los despliegues anteriores y
  *Promote to Production* sobre el anterior es un rollback inmediato, sin rebuild.
- Un cambio del protocolo v1 exige coordinar frontend y Game Server. Regla: **el servidor se despliega
  primero** cuando el cambio es aditivo y compatible; si no lo es, deja de ser v1 y requiere versionado
  explícito del protocolo (`"v": 2`), que está fuera de MVP.

---

## 3. Game Server en VPS

### 3.1 Imagen Docker multi-stage

Build estático de Go, imagen final mínima y usuario no root. **Sin healthcheck en la imagen**: el binario no
tiene subcomando para consultarse a sí mismo y `distroless` no trae shell ni `curl` (ver la nota al final de
esta sección).

> **Estado: propuesta.** No existe todavía `infra/docker/game-server.Dockerfile` en el repositorio. Lo que
> sigue es la especificación del primer Dockerfile, escrita contra la estructura real del módulo Go.

```dockerfile
# infra/docker/game-server.Dockerfile
# ─── Etapa 1: build ─────────────────────────────────────────────────────────
FROM golang:1.27-alpine AS build
WORKDIR /src

# Caché de dependencias: capa estable mientras no cambien go.mod/go.sum
COPY services/game-server/go.mod services/game-server/go.sum ./
RUN go mod download

# Basta con copiar el módulo: los JSON Schema que el servidor embebe con go:embed
# viven DENTRO de él, en internal/protocol/schema/v1/, versionados como espejo de
# packages/protocol. Regenerarlos es tarea de `pnpm run protocol:build`, no del build de la imagen.
COPY services/game-server/ ./

ARG VERSION=dev
ARG COMMIT=unknown
# CGO_ENABLED=0 → binario estático, sin dependencias de libc en la imagen final
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/eo-game-server ./cmd/server

# ─── Etapa 2: imagen final ──────────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/eo-game-server /eo-game-server

# Usuario no root (uid 65532, provisto por la variante :nonroot)
USER nonroot:nonroot

EXPOSE 8080 9090

# Sin HEALTHCHECK: ver la nota de abajo.
ENTRYPOINT ["/eo-game-server"]
```

Por qué cada decisión:

| Decisión | Motivo |
|---|---|
| `CGO_ENABLED=0` | Binario estático; permite una imagen final sin libc y elimina la clase de fallos por versión de glibc. |
| `distroless/static-debian12` | Sin shell, sin gestor de paquetes, sin utilidades: superficie de ataque mínima. Una RCE en el proceso no encuentra herramientas con las que pivotar. |
| `:nonroot` + `USER nonroot` | El proceso no corre como root. No necesita privilegios: no abre puertos < 1024 ni escribe fuera de su propio espacio. |
| `-trimpath -ldflags="-s -w"` | Builds reproducibles y binario más pequeño; no se filtran rutas de la máquina de build. |
| Copiar solo `services/game-server/` | El espejo de los JSON Schema ya está versionado dentro del módulo. Copiar además `packages/protocol/schema/` a otra ruta crearía una segunda copia que `go:embed` no mira. |

**El problema del healthcheck, dicho con claridad.** El binario **no tiene subcomando `healthcheck`** —de
hecho no tiene subcomandos: arranca el servidor y nada más—, y `distroless` no trae `curl` ni shell con la
que emularlo. Es decir: hoy **no se puede declarar un `HEALTHCHECK` dentro de esta imagen**, y con ello
`docker compose up -d --wait` no tiene nada a lo que esperar. Las salidas honestas son tres:

1. **Sondear desde fuera del contenedor**, que es lo que ya hacen el Paso 5 de §6 y la monitorización:
   `curl -fsS http://127.0.0.1:8080/ready` desde el host. Es suficiente para desplegar y verificar.
2. Usar una imagen base con un cliente HTTP mínimo, aceptando algo más de superficie.
3. Añadir el subcomando `healthcheck` al binario, que es la opción limpia y la que devuelve el `--wait`.
   **TBD (fuera de MVP).**

Mientras se elija una, ni el Dockerfile ni el compose deben declarar un `HEALTHCHECK` que no existe: un
healthcheck que apunta a un comando inexistente marca el contenedor como *unhealthy* para siempre.

Igual de importante: **`version` y `commit` inyectados por `-ldflags` no se registran ni se exponen hoy**.
El servidor no imprime su versión al arrancar ni la publica en `/health`. Saber qué SHA corre durante un
incidente sale del registro de despliegues y de la etiqueta de la imagen (§3.2), no del proceso. Cablearlos
al log de arranque y al cuerpo de `/health` es trabajo pendiente: **TBD (fuera de MVP)**.

Build y publicación:

```bash
docker build \
  -f infra/docker/game-server.Dockerfile \
  --build-arg VERSION="$(git describe --tags --always)" \
  --build-arg COMMIT="$(git rev-parse --short HEAD)" \
  -t <registry>/eo-game-server:"$(git rev-parse --short HEAD)" \
  -t <registry>/eo-game-server:latest \
  .
docker push <registry>/eo-game-server:"$(git rev-parse --short HEAD)"
```

**Las etiquetas de despliegue son inmutables y por commit.** `latest` existe por comodidad; el
`docker-compose` de producción referencia **siempre** el SHA, porque un rollback necesita una etiqueta que
no cambie bajo los pies.

### 3.2 Composición en el VPS

```yaml
# /opt/empires-online/docker-compose.yml (en el VPS)
name: empires-online

services:
  game-server:
    image: <registry>/eo-game-server:${EO_IMAGE_TAG}
    container_name: eo-game-server
    env_file: /etc/empires-online/game-server.env   # permisos 0600, propietario root
    ports:
      - "127.0.0.1:8080:8080"    # solo loopback: el proxy es el único que entra
      - "127.0.0.1:9090:9090"    # métricas: solo loopback
    restart: unless-stopped
    stop_grace_period: 30s        # margen para cerrar conexiones y drenar la cola de persistencia
    read_only: true
    cap_drop: ["ALL"]
    security_opt: ["no-new-privileges:true"]
    logging:
      driver: json-file
      options: { max-size: "50m", max-file: "5" }
```

Notas:

- **`ports` atado a `127.0.0.1`.** Sin esto, Docker publica en `0.0.0.0` y **atraviesa las reglas de
  `ufw`/`iptables`** por la cadena `DOCKER`, dejando 8080 y 9090 expuestos a Internet aunque el firewall
  parezca cerrado. Es el error de despliegue más frecuente y más silencioso.
- `read_only: true` y `cap_drop: ALL`: el proceso no escribe en el sistema de archivos ni necesita
  capacidades.
- `stop_grace_period: 30s`: el apagado ordenado tiene un presupuesto propio de 25 s, dentro del cual el
  drenaje de la cola de persistencia dispone de hasta 15 s (§8.3). 30 s deja margen sobre ambos.
- **`EO_IMAGE_TAG` no es configuración del Game Server.** Es una variable de *tooling de despliegue* que solo
  interpola Compose para elegir la etiqueta de la imagen; el proceso Go no la lee y no forma parte del
  catálogo `EO_` de [configuration.md §3](./configuration.md#3-referencia-de-variables). El mismo criterio
  vale para las variables de test `EO_INTEGRATION`, `EO_TEST_POSTGRES_URL` y `EO_TEST_REDIS_URL`: comparten
  prefijo por coherencia de nombres, no por pertenecer a la configuración del servidor. La lista completa
  está en [configuration.md §3.10](./configuration.md#310-variables-que-no-son-configuración-del-servidor).

### 3.3 Supervisión del proceso

Dos opciones válidas; **elige una y documenta cuál**.

**Opción A — restart policy de Docker (recomendada por simplicidad).** `restart: unless-stopped` reinicia el
contenedor ante cualquier salida no solicitada. Requiere que el daemon de Docker arranque con el sistema
(`systemctl enable docker`).

**Opción B — unidad systemd que gobierna Compose.** Da control explícito de orden de arranque, límites de
reinicio y dependencia de la red.

```ini
# /etc/systemd/system/empires-online.service
[Unit]
Description=Empires Online Game Server
Requires=docker.service
After=docker.service network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/opt/empires-online
EnvironmentFile=/opt/empires-online/.image-tag
ExecStart=/usr/bin/docker compose up -d
ExecStop=/usr/bin/docker compose down
TimeoutStartSec=180
Restart=on-failure
RestartSec=10s

[Install]
WantedBy=multi-user.target
```

En ambos casos, hay un límite que respetar: **si el proceso entra en bucle de reinicio, hay que parar y
diagnosticar, no dejarlo reintentar**. Un Game Server que arranca, carga movimientos `ACTIVE`, se cae y
vuelve a arrancar puede reprocesar transiciones. El bucle de reinicio se detecta con
`eo_game_tick_duration_seconds_count` reiniciándose a cero y con la alerta de readiness
(ver [monitoring.md](./monitoring.md#5-reglas-de-alerta-propuestas)).

---

## 4. Reverse proxy y terminación TLS

El proxy termina TLS y hace *upgrade* a WebSocket hacia el Game Server en loopback. La clave de esta
configuración son los **timeouts**: un WebSocket de juego es una conexión larga con periodos de silencio, y
los defaults de cualquier proxy están pensados para HTTP de petición/respuesta.

### 4.1 Caddy (recomendado: TLS automático)

```caddyfile
# /etc/caddy/Caddyfile
game.<dominio> {
    encode zstd gzip

    @ws {
        path /ws
        header Connection *Upgrade*
        header Upgrade    websocket
    }

    handle @ws {
        reverse_proxy 127.0.0.1:8080 {
            # Un WebSocket de juego puede estar en silencio entre acciones del jugador.
            # El servidor hace ping cada 15 s y tiene read timeout de 45 s: el proxy
            # debe ser MÁS TOLERANTE que el servidor, nunca al revés.
            transport http {
                read_timeout   0s     # sin límite: el keepalive lo gobierna el servidor
                write_timeout  0s
                dial_timeout   5s
            }
        }
    }

    handle /health { respond "" 404 }   # liveness no se expone públicamente
    handle /ready  { respond "" 404 }

    handle {
        reverse_proxy 127.0.0.1:8080
    }

    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        X-Content-Type-Options    "nosniff"
        Referrer-Policy           "no-referrer"
    }

    log {
        output file /var/log/caddy/game-access.log
        format json
    }
}
```

Caddy obtiene y renueva el certificado con Let's Encrypt automáticamente, y hace *upgrade* de WebSocket sin
configuración adicional. Requiere que los puertos 80 y 443 sean alcanzables desde Internet para el desafío
ACME.

### 4.2 Nginx (alternativa)

```nginx
# /etc/nginx/conf.d/empires-online.conf
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}

upstream eo_game_server { server 127.0.0.1:8080; keepalive 32; }

server {
    listen 443 ssl http2;
    server_name game.<dominio>;

    ssl_certificate     /etc/letsencrypt/live/game.<dominio>/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/game.<dominio>/privkey.pem;
    ssl_protocols       TLSv1.2 TLSv1.3;

    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;

    location /ws {
        proxy_pass         http://eo_game_server;
        proxy_http_version 1.1;
        proxy_set_header   Upgrade    $http_upgrade;
        proxy_set_header   Connection $connection_upgrade;
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;

        # Timeouts: por encima del read timeout de 45 s del servidor.
        proxy_read_timeout    600s;
        proxy_send_timeout    600s;
        proxy_connect_timeout   5s;
        proxy_buffering       off;   # los deltas deben salir sin acumularse

        # Debe ser >= EO_WS_MAX_MESSAGE_BYTES (16384) para que el rechazo lo emita
        # el servidor con MESSAGE_TOO_LARGE y no el proxy con un cierre opaco.
        client_max_body_size 64k;
    }

    location = /health { return 404; }
    location = /ready  { return 404; }
    location / { proxy_pass http://eo_game_server; }
}
server { listen 80; server_name game.<dominio>; return 301 https://$host$request_uri; }
```

### 4.3 Reglas de timeout que no se negocian

| Parámetro | Valor | Razón |
|---|---|---|
| Ping del servidor (`WSPingInterval`) | 15 s | **Constante de código, no configurable por entorno** ([configuration.md §3.9](./configuration.md#39-constantes-no-configurables-por-entorno)). Mantiene viva la conexión a través de NAT y proxies. |
| Read timeout del servidor (`WSReadTimeout`) | 45 s | Constante de código. Tolera perder dos pings. |
| Write timeout del servidor (`WSWriteTimeout`) | 10 s | Constante de código. Plazo máximo para completar una escritura hacia el cliente. |
| Handshake timeout (`WSHandshakeTimeout`) | 5 s | Constante de código. Plazo para recibir `session.hello`; al agotarse, cierre 4408. El proxy no debe cortar antes. |
| Read/idle timeout del proxy | Sin límite (Caddy) o 600 s (Nginx) | **Debe superar al del servidor.** Si el proxy corta antes, el jugador ve una desconexión sin código de cierre y el servidor no se entera hasta su propio timeout. |
| Tamaño máximo de frame en el proxy | ≥ 16 KiB | Para que el rechazo por tamaño lo emita el servidor con `MESSAGE_TOO_LARGE`, en vez de un corte mudo del proxy. |
| Buffering | Desactivado | Un delta bufferizado es un delta tarde; el juego es en tiempo real. |

---

## 5. Postgres y Redis gestionados

Ambos son **servicios gestionados**, no contenedores en el VPS. Razón: el respaldo, el PITR y el failover de
Postgres son la diferencia entre un incidente y una pérdida de mundo, y no es sensato reimplementarlos.

| | PostgreSQL 16 | Redis 7 |
|---|---|---|
| Rol | *Durable source of truth* | Estado *hot/transient* |
| TLS | Obligatorio **por procedimiento**: `sslmode=require` como mínimo en `EO_POSTGRES_URL`. El cargador de configuración **no** lo comprueba: es casilla del checklist de §9, no fail-fast | Obligatorio **por procedimiento**: esquema `rediss://` en `EO_REDIS_URL`, tampoco validado por el cargador |
| Acceso de red | Lista blanca con la IP del VPS, o peering privado. **Sin acceso público.** | Igual |
| Backups | Sí: completos diarios + PITR con WAL. Ver [backups.md](./backups.md) | **No se respalda.** Es reconstruible por diseño |
| Pérdida total | Incidente mayor: ver [disaster-recovery.md](./disaster-recovery.md#e4-postgres-corrupto) | Degradación temporal: ver [disaster-recovery.md](./disaster-recovery.md#e5-redis-caído-o-vaciado) |

Requisitos adicionales de Postgres: la extensión y la configuración de WAL necesarias para PITR deben estar
activas desde el primer día (no se puede recuperar a un punto anterior a la activación del archivado), y el
rol de la aplicación tiene privilegios sobre su esquema, **no** es superusuario.

Requisitos de Redis: política de expiración que respete los TTL (las claves `presence:player:{playerId}`,
`idem:{playerId}:{requestId}` y `ticket:jti:{jti}` dependen de que el TTL se honre), y **sin** persistencia
requerida — si el proveedor la ofrece, es una comodidad, no una garantía sobre la que se pueda diseñar.

---

## 6. Procedimiento de despliegue

Precondiciones: CI en verde sobre el commit —en la práctica `pnpm run verify`, que encadena
`protocol:build → docs:check → typecheck → test → server:fmt:check → server:vet → server:test`, más
`protocol:check` y los tests de integración con `EO_INTEGRATION=1`, que `verify` **no** ejecuta—, imagen
publicada con la etiqueta del SHA, y backup lógico verificado si el despliegue incluye migraciones
destructivas.

### Paso 1 — Anunciar la ventana

El reinicio del Game Server desconecta a todos los jugadores conectados (§8). Anuncia la ventana con
antelación por el canal habitual del proyecto.

### Paso 2 — Backup previo

```bash
# Snapshot o backup lógico antes de tocar el esquema. Obligatorio si hay migración destructiva.
# Procedimiento completo en backups.md §7.
```

### Paso 3 — Entender que las migraciones las aplica el propio arranque

**No hay un paso de migración separado, porque el binario no tiene subcomando `migrate`.** Las migraciones
están embebidas en el binario con `go:embed` y `postgres.Migrate` las aplica **automáticamente al arrancar**,
antes de abrir ninguna conexión de juego, usando golang-migrate. Consecuencias operativas, que hay que
asumir tal cual:

- **El orden "esquema primero, aplicación después" se cumple igualmente**, pero dentro del mismo arranque:
  la versión nueva migra y luego se pone a servir. Nunca hay código nuevo hablando con un esquema viejo.
- **No se puede migrar sin desplegar, ni desplegar sin migrar.** Para migraciones aditivas eso da igual. Para
  destructivas obliga a la disciplina de dos fases de §7.3, que es la que se quería de todas formas.
- **Un esquema marcado como `dirty`** (una migración anterior que falló a medias) hace que el proceso
  **se niegue a arrancar**, con un mensaje que nombra la versión afectada. Es deliberado: continuar sería
  adivinar. Eso convierte una migración fallida en un despliegue que no levanta, no en un servidor sirviendo
  sobre un esquema a medias.

Verifica el estado del esquema desde el cliente de Postgres, no desde el binario. golang-migrate mantiene
`schema_migrations` con dos columnas, `version` y `dirty`:

```sql
SELECT version, dirty FROM schema_migrations;
```

Si `dirty` es `true`, **para el despliegue aquí** y resuelve la migración antes de continuar
(ver [disaster-recovery.md](./disaster-recovery.md#e7-despliegue-defectuoso)).

Un paso de migración desacoplado del arranque —un subcomando `migrate up` / `migrate status` que permita
migrar con el servidor viejo aún en pie— queda como **TBD (fuera de MVP)**.

### Paso 4 — Desplegar la imagen nueva

```bash
cd /opt/empires-online
echo "EO_IMAGE_TAG=<sha-nuevo>" > .image-tag        # registro del tag activo
docker compose pull
docker compose up -d
```

No se usa `--wait`: la imagen no declara `HEALTHCHECK` (§3.1), así que no habría nada que esperar. La espera
real es el Paso 5, sondeando `/ready` desde el host:

```bash
# Espera activa a que el servidor esté listo, con tope de 120 s
for i in $(seq 1 60); do
  curl -fsS http://127.0.0.1:8080/ready >/dev/null 2>&1 && { echo "ready"; break; }
  sleep 2
done
```

### Paso 5 — Verificar

```bash
# Liveness: el proceso vive
curl -fsS http://127.0.0.1:8080/health && echo " health OK"

# Readiness: postgres + redis + game_loop. Lee el cuerpo: trae el mapa `checks`.
curl -fsS http://127.0.0.1:8080/ready  && echo " ready OK"

# El loop está avanzando de verdad (dos lecturas separadas deben diferir)
curl -fsS http://127.0.0.1:9090/metrics | grep '^eo_game_tick_duration_seconds_count'
sleep 5
curl -fsS http://127.0.0.1:9090/metrics | grep '^eo_game_tick_duration_seconds_count'

# Las migraciones se aplicaron y el mundo se rehidrató sin sorpresas
docker logs eo-game-server 2>&1 | grep -E '"msg":"(migraciones aplicadas|esquema ya al día)"' | tail -1
docker logs eo-game-server 2>&1 | grep '"msg":"mundo rehidratado"' | tail -1
docker logs eo-game-server 2>&1 | grep '"msg":"arrancando Empires Online game server"' | tail -1

# TLS y WSS desde fuera
curl -fsSI https://game.<dominio>/ | head -1
```

La línea `mundo rehidratado` es la más informativa del arranque: trae `units`, `cities`,
`movements_resumed`, `movements_arrived_while_down` y `movements_failed`. Un `movements_failed` distinto de
cero significa polilíneas inválidas en la base y merece investigación, aunque el servidor haya arrancado
bien. El SHA que corre **no** sale de los logs (§3.1): sale de `.image-tag` y del registro de despliegues.

Cierre de la verificación: **una conexión real de un jugador**. Abrir el cliente, comprobar
`session.hello → session.welcome`, recibir `world.snapshot`, emitir un `unit.move` y ver
`unit.move.accepted` seguido de `unit.movement.started` y, al llegar, `unit.movement.completed`. Un
despliegue no está verificado hasta que un movimiento se ha completado de extremo a extremo.

### Paso 6 — Observar 15 minutos

Vigilar en los dashboards (ver [monitoring.md](./monitoring.md#4-dashboards-propuestos)):
`eo_game_tick_overruns_total`, el p99 de `eo_game_tick_duration_seconds`, `eo_persistence_queue_depth`,
`eo_connected_players` recuperándose hasta niveles previos, y la tasa de `system.error` por código.

---

## 7. Rollback

### 7.1 Frontend

*Promote to Production* del despliegue anterior en Vercel. Inmediato, sin rebuild, sin coordinación.

### 7.2 Game Server sin migraciones

```bash
cd /opt/empires-online
echo "EO_IMAGE_TAG=<sha-anterior>" > .image-tag
docker compose pull && docker compose up -d
curl -fsS http://127.0.0.1:8080/ready
```

Duración: la de un reinicio (§8). Por eso las etiquetas por SHA son obligatorias: `latest` no permite volver
atrás.

### 7.3 Game Server con migraciones

Aquí el rollback deja de ser trivial y el diseño de la migración decide si es posible.

| Tipo de migración | Rollback |
|---|---|
| **Aditiva** (columna nueva con default, tabla nueva, índice) | Volver la imagen atrás basta. El código viejo ignora lo que no conoce. No hace falta ejecutar el `.down.sql`. |
| **Destructiva** (columna borrada, tipo cambiado, `CHECK` estrechado) | El `.down.sql` restaura la estructura, **no los datos**. La recuperación de datos exige el backup del Paso 2. |

De ahí la regla de diseño que gobierna todo el ciclo de migraciones: **las migraciones se diseñan en dos
fases**. Primero una migración aditiva y un despliegue que escribe en el formato nuevo mientras sigue
leyendo el viejo; después, en un despliegue posterior, la migración destructiva que elimina lo antiguo.
Entre ambas, el rollback siempre es un cambio de imagen. Detalles en
[../database/migrations.md](../database/migrations.md).

Aquí es donde la regla deja de ser una preferencia y pasa a ser un requisito, porque **no hay forma de
revertir el esquema con las herramientas del propio despliegue**: el binario no expone `migrate down`, y
volver a la imagen anterior **no** deshace las migraciones ya aplicadas — el arranque solo migra hacia
adelante. Si un `.down.sql` tiene que ejecutarse, hoy se ejecuta a mano contra la base, con revisión de otra
persona y backup previo:

```bash
# Con el Game Server DETENIDO. El SQL sale del .down.sql de la migración correspondiente,
# en services/game-server/migrations/, y hay que ajustar schema_migrations en la misma transacción.
cd /opt/empires-online && docker compose down
# ... aplicar el .down.sql y fijar version/dirty en schema_migrations ...
```

Es un procedimiento manual y arriesgado, y esa incomodidad es exactamente la razón para diseñar en dos
fases. Un `migrate down` de primera clase queda como **TBD (fuera de MVP)**.

---

## 8. Estrategia ante reinicio

Un reinicio del Game Server no es un evento excepcional: es la operación normal de cada despliegue. El
sistema está diseñado para soportarlo, y el diseño se apoya en dos propiedades del canon.

### 8.1 Ningún estado durable depende de un WebSocket vivo

El mundo es persistente y evoluciona con o sin jugadores. Cerrar todas las conexiones **no pierde nada
durable**: solo cambia el estado de presencia de los jugadores afectados.

### 8.2 La posición durante un movimiento es analíticamente reconstruible

El path se persiste como polilínea temporizada de waypoints `{x, y, tMs}` en `unit_movements`. La posición
autoritativa en el instante T es el último waypoint con `tMs <= (T - start_time_ms)`. **No hace falta
reproducir ticks**: es una consulta y una comparación.

### 8.3 Secuencia de apagado ordenado

Tal y como está implementada, con un presupuesto total de **25 s**:

1. Se recibe `SIGTERM` o `SIGINT` (lo envía `docker compose down` / `up -d` al recrear).
2. **Se apagan los dos servidores HTTP**, el de juego y el de métricas. Deja de aceptarse tráfico nuevo,
   incluidos los upgrades de WebSocket. A partir de aquí `/health` y `/ready` dejan de responder: no es que
   `/ready` devuelva 503, es que el listener ya no está.
3. Se cierran todas las conexiones WebSocket activas con código **1001** (*going away*) y el motivo
   `servidor en mantenimiento`, para que el cliente sepa que debe reconectar y no lo trate como error.
4. **Se drena la cola de persistencia**, con un tope de **15 s**. Este es el paso crítico: el estado con
   *dirty flag* (posiciones consolidadas, HP) que aún no se ha volcado se escribe ahora. Si el drenaje no
   termina a tiempo, se registra un `error` con el número de trabajos pendientes — esa cifra es exactamente
   lo que se va a perder.
5. Se guarda el tick final en `world_state`, de modo que el mundo retome la cuenta donde la dejó.
6. Se cierran los pools de Postgres y Redis y el proceso sale con código 0.

`stop_grace_period: 30s` en el compose deja margen por encima de esos 25 s. Bajarlo a menos de 25 s
convertiría cada `docker compose down` en un `SIGKILL` a mitad del drenaje.

> **Nota honesta sobre el orden.** Sería preferible que `/ready` pasara a 503 *antes* de cerrar los
> listeners, para que el reverse proxy dejara de enviar conexiones nuevas mientras el servidor drena. Hoy no
> es así: los listeners se cierran de golpe. En un despliegue de proceso único la diferencia práctica es
> pequeña —el corte llega igual—, pero es una mejora real el día que haya balanceo. **TBD (fuera de MVP).**

Si el apagado es abrupto (SIGKILL, corte de energía), se pierde como máximo el trabajo desde el último
flush: `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS=50` a 10 Hz son **5 segundos** de estado eventual. El estado
*write-through* (creación de entidades, inicio y fin de movimiento, ownership, transiciones de presencia) se
**encola en la transacción inmediatamente** tras aplicarse en RAM, y los workers reintentan hasta 3 veces con
backoff. Queda una ventana de riesgo honesta y documentada: si el proceso muere entre la aceptación de un
comando y el COMMIT de su transacción —típicamente pocas decenas de milisegundos—, ese movimiento se pierde
y la unidad se queda en su última posición consolidada. Es un RPO conocido, no un descuido.

### 8.4 Secuencia de arranque

1. `config.Load()` y validación fail-fast (ver [configuration.md](./configuration.md#4-validación-al-arrancar-fail-fast)).
2. **Se aplican las migraciones embebidas** (§6, Paso 3). Un esquema `dirty` aborta el arranque aquí.
3. Se conecta a Postgres y a Redis.
4. **Se carga el mundo:** se lee `world_state` (semilla, dimensiones, `epoch_ms`, tick) y **se regenera el
   terreno desde la semilla**. La copia de `world_chunks` solo se escribe la primera vez, como registro de
   auditoría; en los arranques siguientes no se lee.
5. Se cargan las entidades vivas y **los movimientos con `status = 'ACTIVE'`**, y se rehidrata la simulación:
   - si `arrival_time_ms <= now`: el movimiento se completa inmediatamente, la unidad hace *snap* al tile
     final y el movimiento pasa a `COMPLETED`;
   - si no: se reanuda desde la polilínea, calculando la posición actual con la regla de §8.2;
   - si la polilínea es inválida: el movimiento pasa a `FAILED` y la unidad se queda donde estaba. **Nunca se
     teletransporta a nadie por un dato dudoso.**
   Las murallas de las ciudades existentes vuelven a bloquear su rectángulo 3×3: la capa de ocupación no se
   persiste, se deriva.
6. Arrancan el servidor HTTP, el de métricas y el game loop; `/ready` empieza a devolver 200.
7. Los jugadores reconectan (con *backoff*), obtienen ticket nuevo, envían `session.hello`, reciben
   `session.welcome` y un `world.snapshot` del área de interés (por defecto 5×5 chunks), y a partir de ahí
   deltas.

### 8.5 Ventana de indisponibilidad esperada

| Fase | Duración estimada |
|---|---|
| Apagado ordenado (drenaje incluido) | 2–10 s |
| Arranque: migraciones, generación del terreno, carga de entidades y movimientos activos | 5–20 s |
| Reconexión de los clientes (con *backoff* y jitter) | 5–30 s |
| **Total percibido por el jugador** | **~15–60 s** |

**El terreno se regenera desde `EO_WORLD_SEED` en cada arranque**, y eso está bien: para un mundo de 512×512
es trabajo de CPU pura, más barato que leer 256 filas de `world_chunks`, y garantiza que semilla y mapa nunca
divergen. Lo que sí es señal de bug es un arranque que tarde minutos: apunta a la carga de entidades o a la
base de datos, no al generador.

Qué ve el jugador: la conexión se cierra, el cliente muestra "reconectando", y al volver todo está donde
debe estar — incluidas las unidades que estaban en movimiento, ahora en la posición que les corresponde por
el tiempo transcurrido, no en la que tenían al desconectar. Esa continuidad es el resultado observable del
modelo de polilínea temporizada, y es lo que hay que verificar tras cada despliegue.

Despliegue sin ninguna interrupción (*zero-downtime*) exigiría dos instancias con traspaso de estado
autoritativo, lo que contradice el modelo de proceso único del MVP: **fuera de MVP**.

---

## 9. Checklist de seguridad previo a producción

Ninguna casilla es opcional. Un "no" bloquea el despliegue.

### Secretos

- [ ] `EO_AUTH_JWT_SECRET` de producción generado con entropía criptográfica, **≥ 32 caracteres** y sin la cadena `dev-only`, **distinto** del de staging y del de local. (Estas dos son las únicas condiciones que sí valida el arranque.)
- [ ] Ningún secreto en el repositorio: verificado con un escaneo del historial completo, no solo del HEAD.
- [ ] Ninguna variable `NEXT_PUBLIC_*` contiene credenciales.
- [ ] `/etc/empires-online/game-server.env` con permisos `0600` y propietario root.
- [ ] Rotación de `EO_AUTH_JWT_SECRET` ensayada al menos una vez en staging.

### Red

- [ ] Puertos 8080 y 9090 publicados **solo** en `127.0.0.1`, verificado desde fuera con `nmap` o equivalente.
- [ ] Firewall del VPS: solo 80, 443 y SSH (con puerto o restricción de origen). Verificado **después** de arrancar Docker, porque Docker manipula `iptables`.
- [ ] Postgres y Redis sin acceso público; lista blanca con la IP del VPS o red privada.
- [ ] `EO_POSTGRES_URL` con `sslmode=require` o superior; `EO_REDIS_URL` con esquema `rediss://`. **Comprobación manual: el arranque no la valida.**
- [ ] `EO_METRICS_ADDR` distinto de `EO_HTTP_ADDR`. **Comprobación manual: tampoco la valida el arranque**; un valor repetido falla al abrir el segundo listener, ya en marcha.
- [ ] SSH: solo clave pública, sin contraseña, sin login directo de root.

### TLS y protocolo

- [ ] Certificado válido y renovación automática comprobada.
- [ ] HSTS activo.
- [ ] Solo TLS 1.2 y 1.3.
- [ ] El cliente conecta por `wss://`, nunca por `ws://`, en producción.
- [ ] Comprobación de `Origin` en el upgrade WebSocket: producción acepta **exclusivamente** el dominio del frontend.
- [ ] Timeouts del proxy más permisivos que los del servidor (ping 15 s / read 45 s).
- [ ] Límite de frame del proxy ≥ `EO_WS_MAX_MESSAGE_BYTES` (16384).

### Aplicación

- [ ] Imagen no root, sin shell, `cap_drop: ALL`, `no-new-privileges`, `read_only`.
- [ ] Etiqueta de imagen por SHA de commit, no `latest`.
- [ ] `EO_ENV=production` y `EO_LOG_LEVEL` distinto de `debug`. **El nivel de log es comprobación manual: el arranque no lo valida.**
- [ ] Ninguna verificación automatizada depende de un `HEALTHCHECK` de la imagen ni de un subcomando `migrate` / `healthcheck`: **no existen** (§3.1, §6 Paso 3).
- [ ] Rate limit activo: 20 msg/s con burst 40 por conexión.
- [ ] Idempotencia operativa: `requestId` repetido devuelve la respuesta original y **no** re-ejecuta.
- [ ] Los logs no contienen tickets, DSN completos ni payloads con datos personales.

### Datos y recuperación

- [ ] Backups completos diarios activos y **verificados con una restauración real**. Ver [backups.md](./backups.md).
- [ ] Archivado de WAL activo para PITR **desde antes** del primer jugador.
- [ ] Ensayo de restauración completado y documentado con su duración medida.
- [ ] Ensayo de `FLUSHALL` de Redis en staging: confirmado que degrada y no pierde estado durable.
- [ ] Procedimiento de rollback ensayado en staging, con y sin migraciones.

### Observabilidad

- [ ] `/metrics` accesible solo desde la red de monitorización.
- [ ] Alertas configuradas con destinatario real y **probadas disparándolas a mano**.
- [ ] Dashboards operativos con datos reales de staging.
- [ ] Retención de logs configurada y con rotación (`max-size` / `max-file`).

---

## Referencias

- [configuration.md](./configuration.md) — variables, validación y secretos.
- [monitoring.md](./monitoring.md) — verificación post-despliegue y alertas.
- [backups.md](./backups.md) — backup previo a migración destructiva.
- [disaster-recovery.md](./disaster-recovery.md) — despliegue defectuoso y pérdida del VPS.
- [local-development.md](./local-development.md) — equivalente local sin TLS.
- [../database/migrations.md](../database/migrations.md) — diseño de migraciones en dos fases.
- [../decisions/ADR-012-database-migrations.md](../decisions/ADR-012-database-migrations.md) — migraciones SQL embebidas y aplicadas al arrancar.
- [../decisions/ADR-006-websocket-protocol.md](../decisions/ADR-006-websocket-protocol.md) — protocolo WebSocket JSON versionado.

# Configuración

Referencia completa y normativa de la configuración del Game Server: todas las variables `EO_`, su precedencia, su validación al arrancar, la separación entre infraestructura y gameplay, y el manejo de secretos.

> Regla del canon que gobierna este documento: **ningún valor de gameplay se hardcodea en más de un lugar;
> todo pasa por `internal/config`**. Un literal `300` disperso en el dominio es una violación de diseño,
> aunque coincida con el valor por defecto.

---

## 1. Modelo de configuración

La configuración se carga **una sola vez, al arrancar**, en un struct inmutable producido por
`internal/config`. Ese struct se inyecta explícitamente en los componentes que lo necesitan. No hay
*singleton* global mutable, no hay lecturas de `os.Getenv` fuera de `internal/config`, y no hay recarga en
caliente en MVP.

```
os.Environ()  ──► config.Load() ──► config.Config (inmutable) ──► loop / ws / persistence / pathfinding
                        │
                        └──► validación agregada ──► error fatal si algo falta o está fuera de rango
```

Consecuencia práctica: **el único momento en que el proceso puede rechazar una configuración es el
arranque**. Si `config.Load()` devuelve error, el proceso escribe el motivo en stderr y sale con código
distinto de cero. Nunca arranca "a medias" con valores parciales.

---

## 2. Precedencia y origen de los valores

**El valor efectivo sale del entorno del proceso.** `internal/config` lee con `os.LookupEnv` y aplica los
defaults del código. No hay TOML ni YAML.

Sobre eso hay **una** capa adicional, y sólo una: `config.LoadDotEnv()`, que `cmd/server` y `cmd/migrate`
invocan como primerísimo paso. Busca un archivo `.env` subiendo desde el directorio actual —hasta cinco
niveles, para que funcione igual desde la raíz del repositorio que desde `services/game-server`— y
**exporta al entorno del proceso únicamente las variables que aún no estén definidas**.

```
1. defaults en código  <  2. archivo .env, si existe  <  3. entorno real del proceso
```

| Nivel | Origen | Uso previsto |
|---|---|---|
| 1 | Defaults de `internal/config` (columna *Default* de §3) | Valores del canon; sirven de documentación ejecutable |
| 2 | Archivo `.env` cargado por `config.LoadDotEnv()` | **Sólo comodidad de desarrollo.** Nunca pisa el nivel 3 |
| 3 | Entorno real del proceso (`docker run -e`, `--env-file`, systemd `Environment=`, gestor de secretos) | **Manda siempre.** Es el mecanismo de staging y producción |

Que el nivel 3 gane sobre el 2 no es un detalle: significa que exportar una variable a mano para una
ejecución concreta **siempre** funciona, sin tener que editar ni recordar el archivo. Y que la ausencia de
`.env` no sea un error es lo que permite que el mismo binario arranque en un contenedor donde no hay ninguno.

El parser es deliberadamente tonto: admite comentarios con `#`, el prefijo `export` y comillas simples o
dobles, pero **no expande variables ni procesa escapes**. Un archivo de configuración que se comporta como
un script es una fuente inagotable de sorpresas.

Reglas duras:

- **En staging y producción no se despliega ningún `.env` junto al binario.** Antes esta regla se apoyaba en
  que el proceso no leería el archivo; ahora **sí lo lee**, así que la razón es más fuerte, no más débil: un
  `.env` olvidado en la imagen filtra secretos **y además puede aportar valores** a cualquier variable que el
  entorno real no haya definido. El `Dockerfile` no lo copia y el `.gitignore` no lo versiona; el
  `docker build` usa el contexto del repositorio, donde `.env` está ignorado.
- Como el entorno real tiene prioridad, un `.env` presente **no puede sobrescribir** una variable inyectada
  por el orquestador. El riesgo es de omisión, no de suplantación.
- Una variable presente en el entorno con valor **cadena vacía** se trata como **ausente** y cae al default.
  Para las obligatorias (`EO_POSTGRES_URL`, `EO_AUTH_JWT_SECRET`; y `EO_REDIS_URL` solo en producción) el default es la cadena
  vacía, así que el resultado es el correcto: error de validación y proceso muerto. Nunca se arranca con
  `EO_AUTH_JWT_SECRET=""` aceptando tickets firmados con secreto vacío.
- Una sola vía (entorno) elimina la clase entera de bugs "¿de dónde salió este valor?".

---

## 3. Referencia de variables

Todas las variables del canon §17, **más `EO_WS_OUTBOUND_QUEUE_SIZE`**, que añade la implementación
(§3.8). Los rangos de esta sección son los que `internal/config` valida realmente. Convenciones de la tabla:

- **Tipo**: cómo se parsea. `duration-s` = entero de segundos; `bytes` = entero de bytes.
- **Obligatoria**: sin default; su ausencia es error fatal.
- **Secreto**: nunca se registra en logs, nunca se expone en `/metrics`, nunca llega al cliente.
- **Hot**: recargable en caliente. En MVP, **ninguna** lo es (§4.3).

### 3.1 Entorno y logging

| Variable | Tipo | Default | Rango válido | Secreto | Hot | Efecto |
|---|---|---|---|---|---|---|
| `EO_ENV` | string | `development` | libre; `production` es el único valor con significado especial | No | No | Selecciona el perfil de arranque. En `production` se prohíben los secretos de desarrollo (§4.2). El cargador **no** restringe el conjunto de valores: cualquier cadena distinta de `production` se comporta como un entorno no productivo. |
| `EO_LOG_LEVEL` | string | `info` | `debug` \| `info` \| `warn` \| `error` | No | No | Nivel mínimo de `log/slog`. En `debug` se emiten trazas por comando y por tick, con coste de CPU y volumen apreciables: no usar en producción salvo diagnóstico acotado. |

### 3.2 Red del proceso

| Variable | Tipo | Default | Rango válido | Secreto | Hot | Efecto |
|---|---|---|---|---|---|---|
| `EO_HTTP_ADDR` | addr | `:8080` | `host:port` tal y como lo acepta `net/http` | No | No | Dirección de escucha del servidor HTTP: upgrade WebSocket en `/ws`, `GET /health`, `GET /ready`, `POST /api/auth/register` y `POST /api/auth/login`. Es la superficie **pública** (detrás del reverse proxy). |
| `EO_METRICS_ADDR` | addr | `:9090` | `host:port` | No | No | Dirección de escucha de `/metrics` (Prometheus), en un `http.Server` **separado** a propósito: permite exponer 8080 al mundo y dejar 9090 accesible solo desde la red de monitorización. Ponerla igual que `EO_HTTP_ADDR` no lo detecta la validación: falla al abrir el segundo listener. |

### 3.3 Dependencias

| Variable | Tipo | Default | Rango válido | Secreto | Hot | Efecto |
|---|---|---|---|---|---|---|
| `EO_POSTGRES_URL` | URL | — (**obligatoria**) | esquema `postgres://`, `postgresql://`, `pgx://` o `pgx5://` | **Sí** | No | DSN de la *durable source of truth*. Rige el pool de conexiones y toda la persistencia. También lo usa el migrador, que reescribe el esquema a `pgx5` para golang-migrate. En producción debe llevar `sslmode=require` o superior — es una regla operativa, **no** la comprueba el cargador. |
| `EO_REDIS_URL` | URL | — (**obligatoria solo en producción**) | `redis://` o `rediss://`, o **vacía** | **Sí** (contiene contraseña) | No | DSN del estado *hot/transient*: presencia (`presence:player:{playerId}`), idempotencia (`idem:{playerId}:{requestId}`) y anti-replay del ticket (`ticket:jti:{jti}`). **Vacía en desarrollo** conmuta a `internal/persistence/memory`: mismas garantías con un solo proceso, y el arranque lo avisa con un `WARN`. Con `EO_ENV=production` y vacía, el cargador **falla**: un almacén en proceso no se comparte entre instancias y el anti-replay dejaría de valer (INV-SEC-003). En producción debe usarse `rediss://` (TLS) — regla operativa, no validada por el cargador. |
| `EO_AUTH_JWT_SECRET` | string | — (**obligatoria**) | **≥ 32 caracteres**; no puede contener `dev-only` si `EO_ENV=production` | **Sí** | No | Clave HS256 con la que se firma el *game ticket* y con la que el Game Server lo verifica. Debe ser **idéntica** en emisor y verificador. Rotación en §6.3. |

### 3.4 Simulación y mundo

| Variable | Tipo | Default | Rango válido | Secreto | Hot | Efecto |
|---|---|---|---|---|---|---|
| `EO_TICK_RATE_HZ` | int | `10` | 1–1000, **y debe dividir exactamente a 1000** | No | No | Frecuencia del game loop. Período = `1000 / EO_TICK_RATE_HZ` ms; con el default, **100 ms**. Define la resolución temporal de todas las fases del tick y el umbral de `eo_game_tick_overruns_total`. Subirla multiplica el coste de CPU y el volumen de deltas. |
| `EO_WORLD_WIDTH` | int | `512` | 16–65536, **múltiplo exacto de `EO_CHUNK_SIZE`** | No | No | Ancho del mundo en tiles (eje X, hacia el este). |
| `EO_WORLD_HEIGHT` | int | `512` | 16–65536, **múltiplo exacto de `EO_CHUNK_SIZE`** | No | No | Alto del mundo en tiles (eje Y, hacia el sur). |
| `EO_WORLD_SEED` | int64 | `20260909` | cualquier int64 | No | No | Semilla de la generación determinista del terreno. **El mapa se regenera desde la semilla en cada arranque**; `world_chunks` guarda una copia para auditoría y para permitir mapas editados en el futuro, pero no es la fuente primaria. Cambiar la semilla sobre un mundo ya poblado produce un terreno distinto bajo entidades colocadas con el anterior: no se hace. |
| `EO_CHUNK_SIZE` | int | `32` | 1–256 | No | No | Lado del chunk en tiles. Con 512×512 y chunk 32 salen 16×16 = **256 chunks**. El cargador **no** exige potencia de 2, solo que el mundo sea múltiplo exacto del chunk. |
| `EO_INTEREST_RADIUS_CHUNKS` | int | `2` | 0–64 | No | No | Radio de suscripción alrededor del centro de vista, en chunks. Con 2, el área de interés es 5×5 = 25 chunks. Subirlo aumenta cuadráticamente el tamaño de `world.snapshot` y el volumen de deltas por jugador. |

### 3.5 Presencia y protección

| Variable | Tipo | Default | Rango válido | Secreto | Hot | Efecto |
|---|---|---|---|---|---|---|
| `EO_PRESENCE_TTL_SECONDS` | duration-s | `30` | 1–3600, y **estrictamente mayor** que `EO_PRESENCE_HEARTBEAT_SECONDS` | No | No | Dos usos: TTL de `presence:player:{playerId}` en Redis, y **`DisconnectGrace` del game loop**. La transición a `OFFLINE_PENDING` la decide el loop en RAM comparando contra este valor, **no** leyendo la expiración de la clave de Redis. Un jugador con varias sesiones abiertas sigue `ONLINE` mientras le quede una. |
| `EO_PRESENCE_HEARTBEAT_SECONDS` | duration-s | `10` | 1–3600, y **estrictamente menor** que `EO_PRESENCE_TTL_SECONDS` | No | No | Cadencia con la que el servidor refresca la clave de presencia. También es el `heartbeatIntervalMs` que se anuncia al cliente en `session.welcome`. La relación 10/30 da margen para perder dos heartbeats seguidos sin declarar ausente a nadie. |
| `EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS` | duration-s | `300` | 0–86400 | No | No | Tiempo en `OFFLINE_PENDING` antes de transicionar a `PROTECTED`. Constante **de gameplay**: define la ventana en la que una ciudad recién abandonada sigue siendo atacable. Ver [../specs/presence.md](../specs/presence.md). |

### 3.6 Persistencia

| Variable | Tipo | Default | Rango válido | Secreto | Hot | Efecto |
|---|---|---|---|---|---|---|
| `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` | int | `50` | 1–100000 | No | No | Cada cuántos ticks se vuelca el conjunto *dirty* (posiciones consolidadas de unidades, HP). Con 10 Hz, 50 ticks = **5 s**. Es directamente la **ventana máxima de pérdida de estado eventual** ante un crash: bajarlo reduce esa ventana y sube la carga de escritura; subirlo, al revés. No afecta al estado write-through, que se encola inmediatamente. |
| `EO_MIGRATE_ON_START` | bool | `true` fuera de producción, `false` con `EO_ENV=production` | `true/false`, `1/0`, `yes/no`, `on/off` | No | No | Si el servidor aplica las migraciones pendientes al arrancar. Cómodo en desarrollo; en producción se desactiva porque varias instancias levantándose a la vez competirían por el mismo esquema — allí se ejecuta el binario `cmd/migrate` como paso previo del despliegue. Un valor ilegible es un error, no un silencio. Ver [ADR-012](../decisions/ADR-012-database-migrations.md). |

### 3.7 Pathfinding

| Variable | Tipo | Default | Rango válido | Secreto | Hot | Efecto |
|---|---|---|---|---|---|---|
| `EO_PATHFINDING_MAX_NODES` | int | `20000` | 100–10000000 | No | No | Tope de nodos expandidos por búsqueda A\*. Al excederse se rechaza con `PATH_TOO_LONG`. Es la defensa principal contra que un jugador consuma CPU del tick con destinos patológicos. |
| `EO_PATHFINDING_MAX_DISTANCE` | int | `256` | 1–65536 | No | No | Distancia máxima admitida entre origen y destino, en tiles. Al excederse: `PATH_TOO_LONG`, **antes** de ejecutar la búsqueda. |

### 3.8 WebSocket

| Variable | Tipo | Default | Rango válido | Secreto | Hot | Efecto |
|---|---|---|---|---|---|---|
| `EO_WS_MAX_MESSAGE_BYTES` | bytes | `16384` | 256–4194304 | No | No | Tamaño máximo de mensaje entrante (16 KiB con el default). Superarlo produce `MESSAGE_TOO_LARGE`. Debe ser ≤ al límite del reverse proxy, o el proxy cortará antes y el cliente verá un cierre opaco en vez del código de error. |
| `EO_WS_RATE_LIMIT_PER_SECOND` | int | `20` | 1–10000 | No | No | Tasa sostenida de mensajes por conexión. Al superarse: `RATE_LIMITED` y, si persiste, cierre `4429`. |
| `EO_WS_RATE_LIMIT_BURST` | int | `40` | 1–20000, y **≥ `EO_WS_RATE_LIMIT_PER_SECOND`** | No | No | Capacidad del *token bucket*: ráfaga tolerada por encima de la tasa sostenida. Cubre el pico legítimo de un jugador arrastrando la vista (`session.view`) sin castigarlo. |
| `EO_AUTH_RATE_LIMIT_PER_MINUTE` | int | `10` | 1–100000 | No | No | Peticiones por minuto y por dirección de origen a `POST /api/auth/register` y `POST /api/auth/login`. Por minuto y no por segundo porque es la unidad natural de un intento de login. **No es una comodidad**: el alta ejecuta bcrypt sin autenticación previa, así que sin límite es un amplificador de denegación de servicio apuntando al proceso que corre el game loop. |
| `EO_AUTH_RATE_LIMIT_BURST` | int | `5` | 1–10000 | No | No | Capacidad del cubo de fichas de esos endpoints. Un humano que se equivoca de contraseña dos veces no lo nota; un script sí. |
| `EO_TRUST_PROXY_HEADERS` | bool | `false` | `true`/`false` | No | No | Si se cree a `X-Forwarded-For` / `X-Real-IP` al identificar al cliente. **Actívalo sólo con un proxy inverso delante.** Con proxy y sin esto, todas las peticiones parecen venir del proxy y comparten un único cupo; sin proxy y con esto, cualquiera falsifica la cabecera y se salta el límite. El valor por defecto es el seguro, no el cómodo. |
| `EO_WS_OUTBOUND_QUEUE_SIZE` | int | `256` | 8–65536 | No | No | Tamaño de la **cola de salida por conexión**. Es la contrapresión del lado servidor: si un cliente no consume sus deltas y la cola se llena, la sesión se cierra con **4500** y el cliente reconecta pidiendo un snapshot limpio. Subirlo tolera clientes más lentos a costa de memoria por conexión; bajarlo corta antes. **No aparece en `.env.example`**: se toma el default salvo que se declare. |

### 3.9 Constantes NO configurables por entorno

Estos cuatro tiempos del transporte WebSocket **están fijados en código** (`internal/config`), no se leen del
entorno y no hay variable `EO_` que los cambie. Cambiarlos exige recompilar, y por eso el reverse proxy tiene
que ser más tolerante que ellos (ver [deployment.md](./deployment.md#43-reglas-de-timeout-que-no-se-negocian)):

| Constante | Valor | Papel |
|---|---|---|
| `WSHandshakeTimeout` | **5 s** | Plazo máximo para que el cliente envíe `session.hello` tras el upgrade. Al agotarse: cierre **4408**. |
| `WSPingInterval` | **15 s** | Cadencia del ping del servidor. Mantiene viva la conexión a través de NAT y proxies. |
| `WSReadTimeout` | **45 s** | Silencio máximo tolerado en la lectura. Da margen para perder dos pings seguidos. |
| `WSWriteTimeout` | **10 s** | Plazo máximo para completar una escritura hacia el cliente. |

### 3.10 Variables que no son configuración del servidor

| Variable | Ámbito | Notas |
|---|---|---|
| `EO_INTEGRATION` | Solo tests | Gate del nivel *integration*: con `EO_INTEGRATION=1` se ejecutan; sin él, `t.Skip`. Además hace falta la etiqueta de compilación `integration`. **No la lee `internal/config`.** |
| `EO_TEST_POSTGRES_URL` | Solo tests | DSN de la base contra la que corren los tests de integración. Si está vacía se usa el valor por defecto del propio test, `postgres://empires:empires_dev_password@localhost:5432/empires?sslmode=disable`. **No la lee `internal/config`.** |
| `EO_TEST_REDIS_URL` | Solo tests | DSN de Redis para los tests de integración; por defecto `redis://localhost:6379/1` — la base **1**, no la 0 que usa `EO_REDIS_URL`, para no pisar el estado de desarrollo. **No la lee `internal/config`.** |
| `NEXT_PUBLIC_GAME_SERVER_WS_URL` | Solo frontend | URL del WebSocket, tal y como aparece en `.env.example`. Al llevar prefijo `NEXT_PUBLIC_` **se incrusta en el bundle del navegador**: es información pública por construcción y jamás debe contener credenciales. **No forma parte del catálogo `EO_` del Game Server** y `internal/config` no la lee. Ver [deployment.md](./deployment.md#2-frontend-nextjs-en-vercel). |
| `EO_IMAGE_TAG` | Solo despliegue | Etiqueta (SHA de commit) de la imagen que referencia el `docker-compose.yml` del VPS. Es una variable **de tooling de despliegue**, no de configuración del servidor: el proceso Go no la lee. Ver [deployment.md](./deployment.md#32-composición-en-el-vps). |

Otras constantes que **no** son configuración porque viven en código o en datos: `TILE_W = 64` y
`TILE_H = 32` (proyección isométrica, exclusiva del cliente); √2 en punto fijo `1414214/1000000` para el paso
diagonal; las escalas de coste del A\* `costScaleOrtho = 1000` y `costScaleDiag = 1414`, con la heurística
octile ponderada por `MinTerrainCostUnits = 6` (ROAD); los códigos de cierre WS
`4400/4401/4403/4408/4429/4500`; el TTL de 60 s del *game ticket* y el de 120 s de `ticket:jti:{jti}`; el TTL
de 300 s de `idem:{playerId}:{requestId}`; el tamaño de la cola de comandos del loop (8192); el presupuesto
de apagado ordenado (25 s, con 15 s para drenar la cola de persistencia) y el umbral de 5 s tras el cual
`/ready` marca el game loop como `stale`; y las eras, que viven en la tabla `eras`.

---

## 4. Validación al arrancar: fail-fast

### 4.1 Principio

El proceso **no arranca** con una configuración inválida. Nada de defaults silenciosos para valores
obligatorios, nada de "clamp" callado de un valor fuera de rango, nada de warnings que nadie lee. Un valor
mal puesto tiene que producir un proceso muerto y un mensaje que diga exactamente qué corregir.

La razón es operativa: un Game Server autoritativo que arranca con `EO_TICK_RATE_HZ=0` o con un
`EO_AUTH_JWT_SECRET` vacío no falla de forma visible, falla de forma **sutil** — y en un mundo persistente
las consecuencias se escriben en Postgres antes de que nadie lo note.

### 4.2 Orden de validación

1. **Parseo por tipo y rango individual**, variable a variable, según §3. Un entero que no parsea produce
   `EO_X="…" no es un entero válido`; uno fuera de rango, `EO_X=N fuera del rango permitido [min, max]`. En
   ambos casos el valor efectivo cae al default y el error queda anotado.
2. **Presencia de obligatorias.** `EO_POSTGRES_URL` y `EO_AUTH_JWT_SECRET`: ausente o vacía → error.
   `EO_REDIS_URL` solo es obligatoria si `EO_ENV=production`.
3. **Las seis validaciones cruzadas**, las que ninguna variable puede comprobar sola:

   | # | Regla | Por qué existe |
   |---|---|---|
   | 1 | `EO_AUTH_JWT_SECRET` de **al menos 32 caracteres** | Por debajo de eso, HS256 se firma con una clave adivinable. |
   | 2 | `EO_AUTH_JWT_SECRET` **no puede contener `dev-only`** si `EO_ENV=production` | Es la cadena del secreto de `.env.example`: convierte el despliegue accidental de la plantilla en un proceso muerto en vez de en una llave maestra pública. |
   | 3 | `EO_TICK_RATE_HZ` debe **dividir exactamente a 1000** | Si no, el tick no dura un número entero de milisegundos y toda la aritmética temporal del movimiento arrastra un error acumulativo. |
   | 4 | `EO_WORLD_WIDTH` y `EO_WORLD_HEIGHT` deben ser **múltiplos exactos** de `EO_CHUNK_SIZE` | Un mundo que no encaja en chunks enteros deja una franja sin chunk al que suscribirse. |
   | 5 | `EO_PRESENCE_HEARTBEAT_SECONDS` **estrictamente menor** que `EO_PRESENCE_TTL_SECONDS` | Con heartbeat ≥ TTL la presencia expiraría entre latidos y los jugadores conectados parpadearían a ausentes. |
   | 6 | `EO_WS_RATE_LIMIT_BURST` **≥** `EO_WS_RATE_LIMIT_PER_SECOND` | Un *bucket* con capacidad menor que su tasa de recarga no puede sostener siquiera la tasa nominal. |

**La validación acumula TODOS los errores y los reporta juntos**, no de uno en uno. Arreglar la
configuración de un despliegue con un reinicio por error es tiempo de indisponibilidad regalado.

Reglas que **no** aplica el cargador, y que por tanto son responsabilidad del procedimiento de despliegue
(ver el checklist de [deployment.md](./deployment.md#9-checklist-de-seguridad-previo-a-producción)):
`sslmode=require` en `EO_POSTGRES_URL`, esquema `rediss://` en `EO_REDIS_URL`, `EO_LOG_LEVEL != debug` en
producción y `EO_METRICS_ADDR != EO_HTTP_ADDR`. No están automatizadas: si se te olvidan, el proceso arranca.

### 4.3 Mensajes de error

Formato: qué variable, qué valor se recibió, qué se esperaba, todo en una lista bajo una sola cabecera. El
valor de una variable secreta **nunca** se imprime.

```text
fallo fatal: configuración inválida:
  - EO_AUTH_JWT_SECRET es obligatoria
  - EO_TICK_RATE_HZ=0 fuera del rango permitido [1, 1000]
  - EO_WORLD_WIDTH (500) y EO_WORLD_HEIGHT (512) deben ser múltiplos exactos de EO_CHUNK_SIZE (32)
```

```text
fallo fatal: configuración inválida:
  - EO_AUTH_JWT_SECRET debe tener al menos 32 caracteres
```

```text
fallo fatal: configuración inválida:
  - EO_AUTH_JWT_SECRET tiene el valor de desarrollo en un entorno de producción
```

El proceso escribe esto en stderr y sale con estado 1. Este comportamiento se testea: `internal/config`
tiene casos para los valores por defecto, para las obligatorias ausentes y para **todas** las validaciones
cruzadas, con la aserción sobre el error devuelto.

### 4.4 Hot reload

**Ninguna variable es recargable en caliente en MVP.** Cambiar cualquier valor exige reiniciar el proceso, y
un reinicio del Game Server tiene un procedimiento propio y consecuencias para los jugadores conectados:
ver [deployment.md](./deployment.md#8-estrategia-ante-reinicio) y
[disaster-recovery.md](./disaster-recovery.md#e1-crash-del-game-server).

Razón de fondo: el struct de configuración es inmutable y compartido sin sincronización con el loop de
simulación. Hacerlo mutable exigiría sincronización en la ruta caliente del tick, o *snapshots* por tick, y
ninguna de las dos cosas se justifica para el único candidato razonable (`EO_LOG_LEVEL`). Un endpoint de
cambio de nivel de log en caliente queda como **TBD (fuera de MVP)**.

---

## 5. Infraestructura frente a gameplay

### 5.1 Dos familias con reglas distintas

| | Configuración de infraestructura | Constantes de gameplay |
|---|---|---|
| Ejemplos | `EO_POSTGRES_URL`, `EO_REDIS_URL`, `EO_HTTP_ADDR`, `EO_METRICS_ADDR`, `EO_LOG_LEVEL`, `EO_ENV`, `EO_WS_OUTBOUND_QUEUE_SIZE` | `EO_TICK_RATE_HZ`, `EO_WORLD_*`, `EO_CHUNK_SIZE`, `EO_INTEREST_RADIUS_CHUNKS`, `EO_PRESENCE_*`, `EO_CITY_OFFLINE_PROTECTION_COOLDOWN_SECONDS`, `EO_PATHFINDING_*`, `EO_WS_MAX_MESSAGE_BYTES`, `EO_WS_RATE_LIMIT_*` |
| Difiere entre entornos | Sí, siempre | No: local, staging y producción deben compartir valores |
| Cambiarla altera el comportamiento observable del juego | No | **Sí** |
| Cambiarla invalida tests de simulación | No | **Sí** |
| Requiere ADR para cambiar el default | No | **Sí** |

La distinción importa porque tienen ciclos de vida opuestos. La primera familia *debe* variar por entorno.
La segunda *no debe*: si el cooldown de protección son 300 s en producción y 10 s en local, el
comportamiento que se prueba no es el que se despliega, y los tests de simulación dejan de significar algo.
La forma legítima de acortar tiempos en un test es **inyectar `FakeClock`**, no cambiar la constante.

### 5.2 Por qué ninguna constante de gameplay se hardcodea

1. **Punto único de verdad.** El canon fija que ningún valor de gameplay se hardcodea en más de un lugar.
   Un `300` en el dominio y otro en un test es una divergencia esperando a ocurrir.
2. **Balanceo sin recompilar.** Ajustar el cooldown de protección o el radio de interés es una decisión de
   producto que debe poder tomarse con un reinicio, no con un ciclo de build.
3. **Testabilidad.** Los tests construyen un `config.Config` explícito con los valores del caso. Si el
   dominio leyera literales, no habría manera de probar el borde.
4. **Auditoría.** El valor efectivo se registra al arrancar (log estructurado, secretos redactados), de modo
   que ante un incidente se puede reconstruir con qué reglas corría el mundo en ese momento.

Los valores que **sí** viven en datos, no en configuración, siguen la misma lógica llevada al extremo: las
eras (`STONE_AGE` 20, `BRONZE_AGE` 50, `IRON_AGE` 100, `CASTLE_AGE` 150) están en la tabla `eras`, sembradas
por la migración `000002_seed_catalogs`, no en código ni en variables de entorno; los costes de terreno en
unidades enteras (`GRASSLAND` 10, `FOREST` 16, `HILL` 18, `ROAD` 6; `MOUNTAIN` y `WATER` no transitables) y
el `baseMsPerTile` de cada tipo de unidad (`VILLAGER` = 600 ms) son propiedades del dominio definidas en un
solo sitio. Ver [../database/schema.md](../database/schema.md).

### 5.3 Registro de la configuración efectiva

Al arrancar, y **antes de tocar ninguna dependencia**, el servidor emite una línea de log de nivel `info`
con la configuración de simulación ya validada. Ningún secreto aparece en ella:

```json
{"level":"info","msg":"arrancando Empires Online game server","env":"production","tick_rate_hz":10,"world":"512x512","chunk_size":32}
```

Después llegan las líneas del resto del arranque, útiles para reconstruir un incidente: `migraciones
aplicadas` (con `from` y `to`) o `esquema ya al día`, `mundo generado y persistido` / `mundo existente
cargado` (con `seed`, `tick` y `epoch_ms`), `mundo rehidratado` (con unidades, ciudades, movimientos
reanudados, llegados durante la caída y fallidos), y las dos de escucha con sus direcciones efectivas.

Lo que ese registro **no** incluye hoy es una huella de los secretos que permita comparar dos despliegues
sin revelarlos —por ejemplo, las primeras posiciones del SHA-256 de `EO_AUTH_JWT_SECRET`—. Sería útil
exactamente en el escenario de §6.3 (verificar que emisor y verificador comparten clave tras una rotación);
mientras no exista, esa verificación se hace probando una conexión real. Queda como **TBD (fuera de MVP)**.

---

## 6. Manejo de secretos

Secretos del sistema: `EO_AUTH_JWT_SECRET`, y las credenciales embebidas en `EO_POSTGRES_URL` y
`EO_REDIS_URL`. No hay más en MVP.

### 6.1 Reglas innegociables

1. **Nunca en el repositorio.** Ni en `.env`, ni en `ci.yml`, ni en un comentario, ni en un fixture de test.
   `.env` está en `.gitignore`; lo versionado es `.env.example` con un marcador explícito
   (`dev-only-insecure-secret-change-me-before-any-deployment`), elegido para que contenga la cadena
   `dev-only` y el arranque lo rechace en `EO_ENV=production`. El `docker-compose.yml` de la raíz sí lleva
   la contraseña de desarrollo de Postgres (`empires_dev_password`) en claro: es un valor local por
   definición, no viaja a ningún entorno desplegado.
2. **Nunca en el cliente.** Todo lo que lleva prefijo `NEXT_PUBLIC_` se incrusta en el bundle del navegador.
   `EO_AUTH_JWT_SECRET` se usa **solo** en el runtime del servidor de Next (API route que emite el ticket) y
   en el Game Server. Un secreto en una variable `NEXT_PUBLIC_` es una fuga inmediata y total: cualquiera
   podría firmar tickets para cualquier `playerId`.
3. **Nunca en logs ni en métricas.** El logger redacta; `/metrics` no expone etiquetas derivadas de
   credenciales. Un DSN completo en un mensaje de error de conexión es la fuga más común: los errores de
   conexión se reportan con host y base, sin usuario ni contraseña.
4. **Nunca en la URL.** Ni en query strings, ni en el path del WebSocket. El *game ticket* viaja en el
   **payload** de `session.hello`, no como parámetro de la URL de conexión, precisamente para que no acabe en
   los access logs del reverse proxy.
5. **Distintos por entorno.** Local, staging y producción tienen secretos independientes. Un secreto de
   desarrollo que funcione en producción convierte cada máquina de desarrollo en una llave maestra.

### 6.2 Dónde vive cada secreto

| Entorno | Almacén | Inyección |
|---|---|---|
| Local | Archivo `.env` (ignorado por git), generado por el desarrollador a partir de `.env.example` | Exportado al entorno por la shell o el tooling **antes** de arrancar el proceso; `config.Load()` solo lee el entorno (§2) |
| Vercel (frontend) | *Environment Variables* del proyecto, ámbito Production/Preview/Development | Runtime del servidor de Next |
| VPS (Game Server) | Archivo de entorno con permisos `0600` propiedad de root, referenciado desde la unidad systemd o `--env-file` de Docker | Entorno del proceso |
| Postgres / Redis gestionados | Credenciales generadas por el proveedor | Referenciadas en `EO_POSTGRES_URL` / `EO_REDIS_URL` |

El archivo de entorno del VPS nunca se copia a la imagen Docker. Ver
[deployment.md](./deployment.md#3-game-server-en-vps).

### 6.3 Rotación de `EO_AUTH_JWT_SECRET`

**Lo primero, entender qué se ve afectado y qué no.** El *game ticket* se verifica **una sola vez**, en el
handshake: el servidor comprueba firma, `exp` y `aud`, consume el `jti` en Redis y, a partir de ahí, la
autorización de la conexión reside en la sesión establecida. Por tanto:

- **Las sesiones WebSocket ya establecidas NO se ven afectadas por una rotación.** Nadie es expulsado.
- Lo único afectado son las **conexiones nuevas** durante la ventana de propagación: si el emisor (Next.js)
  y el verificador (Game Server) no comparten el mismo secreto, el ticket falla y la conexión se cierra con
  `4401` / `UNAUTHORIZED`.
- Como el ticket tiene **TTL de 60 s**, cualquier ticket emitido con el secreto viejo caduca solo. No hay
  arrastre largo.

**Procedimiento (rotación planificada):**

1. Genera el nuevo secreto (**≥ 32 caracteres**, con entropía criptográfica: `openssl rand -base64 48`) y
   anota su huella SHA-256 abreviada en el registro de rotaciones.
2. **Anuncia una ventana corta.** Elige una franja de bajo tráfico. El objetivo es que la ventana en la que
   los dos lados difieren dure segundos, no minutos.
3. Actualiza el secreto en Vercel (emisor) y en el archivo de entorno del VPS (verificador) **antes** de
   reiniciar nada. Escribir la variable no la activa: la activación es el reinicio.
4. Reinicia el Game Server (`docker compose up -d --force-recreate` o `systemctl restart`) y, en paralelo,
   dispara el redeploy del frontend. La ventana efectiva es el desfase entre ambas activaciones.
5. Verifica: `/ready` en 200 y **una conexión de prueba completa** (`session.hello` → `session.welcome`).
   Esa conexión es hoy la única comprobación real de que emisor y verificador comparten clave, porque el
   servidor no registra una huella del secreto (§5.3).
6. Vigila `eo_ws_messages_total` y la tasa de cierres `4401` durante 10 minutos. Un repunte sostenido
   significa que un lado quedó con el secreto viejo.
7. Revoca el secreto anterior en el almacén y registra la rotación con fecha, motivo y huellas.

**Impacto esperado:** los jugadores conectados no notan nada. Un jugador que intente conectar exactamente
dentro de la ventana recibe `4401`; el cliente debe reintentar pidiendo un ticket nuevo con *backoff*, y en
el segundo intento entra. Ese reintento es una obligación del cliente, no un extra.

**Rotación de emergencia (secreto comprometido):** se salta el paso 2, se ejecuta inmediatamente, y además
se invalidan las sesiones activas: reiniciar el Game Server cierra todas las conexiones y fuerza a todo el
mundo a obtener un ticket nuevo con el secreto nuevo. La indisponibilidad es la de un reinicio normal
(ver [deployment.md](./deployment.md#8-estrategia-ante-reinicio)), y es el precio correcto a pagar ante un
compromiso.

**Rotación sin ninguna ventana de fallo** exigiría que el verificador aceptase simultáneamente dos claves
(la actual y la anterior), lo que implicaría una variable adicional que el canon no define. Queda como
**TBD (fuera de MVP)**.

### 6.4 Rotación de credenciales de Postgres y Redis

El proveedor gestionado permite crear un segundo usuario antes de retirar el primero, así que aquí sí hay
solapamiento real:

1. Crea el nuevo rol con los mismos privilegios.
2. Actualiza `EO_POSTGRES_URL` / `EO_REDIS_URL` en el archivo de entorno del VPS.
3. Reinicia el Game Server siguiendo el procedimiento de despliegue.
4. Verifica `/ready` y las métricas `eo_database_latency_seconds` / `eo_redis_latency_seconds`.
5. Elimina el rol antiguo **solo después** de comprobar que no queda ninguna conexión suya activa.

---

## Referencias

- [local-development.md](./local-development.md) — `.env.example` y su derivación.
- [deployment.md](./deployment.md) — inyección de configuración en Vercel y en el VPS.
- [monitoring.md](./monitoring.md) — qué métricas vigilar tras un cambio de configuración.
- [../architecture/game-loop.md](../architecture/game-loop.md) — efecto de `EO_TICK_RATE_HZ` y `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS`.
- [../specs/presence.md](../specs/presence.md) — semántica de las variables `EO_PRESENCE_*` y del cooldown de protección.
- [../decisions/ADR-010-authentication-game-ticket.md](../decisions/ADR-010-authentication-game-ticket.md) — el *game ticket* que firma `EO_AUTH_JWT_SECRET`.
- [../decisions/ADR-007-game-loop-frequency.md](../decisions/ADR-007-game-loop-frequency.md) — por qué `EO_TICK_RATE_HZ=10`.

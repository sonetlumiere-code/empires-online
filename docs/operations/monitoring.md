# Observabilidad y monitorización

Logging estructurado, catálogo completo de las 14 métricas Prometheus con sus etiquetas exactas, semántica de `/health` y `/ready`, dashboards y reglas de alerta **propuestos**, y runbook de primer diagnóstico.

> **Qué existe y qué no.** Lo que está implementado y corriendo es: el logging estructurado (§1), las 14
> métricas de §2 expuestas en `EO_METRICS_ADDR/metrics`, y los endpoints `/health` y `/ready` de §3. Los
> dashboards (§4) y las reglas de alerta (§5) son **propuestas**: no hay Prometheus raspando, ni Grafana, ni
> Alertmanager. El runbook (§6) sirve igual consultando `/metrics` a mano.

> Principio operativo: un MMORTS persistente falla de forma **lenta y silenciosa** antes de fallar de forma
> ruidosa. El tick que empieza a ir 5 ms tarde, la cola de persistencia que crece 3 elementos por minuto y
> la latencia de Postgres que sube de 2 a 8 ms no tiran el servicio hoy; lo tiran el sábado por la noche.
> Por eso las señales de este documento son en su mayoría **tendencias**, no umbrales binarios.

---

## 1. Logging estructurado

### 1.1 Formato

JSON por línea, emitido con `log/slog`, a `stdout`. La recolección la hace el driver de logs de Docker
(`json-file` con rotación, ver [deployment.md](./deployment.md#32-composición-en-el-vps)). El proceso no
escribe archivos: el sistema de archivos del contenedor es de solo lectura.

### 1.2 Campos estándar

| Campo | Tipo | Siempre presente | Significado |
|---|---|---|---|
| `ts` | RFC 3339 | Sí | Instante de emisión (reloj de pared del proceso). El nombre es `ts`, no `time`: el handler renombra el campo estándar de `log/slog` |
| `level` | `DEBUG` \| `INFO` \| `WARN` \| `ERROR` | Sí | Nivel; el umbral lo fija `EO_LOG_LEVEL` |
| `msg` | string estable | Sí | Identificador humano del evento. **Estable**: se usa para filtrar, así que no se reescribe a la ligera |
| `service` | string | Sí | Constante `game-server`, añadida al logger raíz |
| `env` | string | Sí | Valor de `EO_ENV`, añadido al logger raíz |
| `player_id` | uuid | Cuando hay jugador en contexto | `players.id` |
| `session_id` | string | Cuando hay sesión | Identificador de la sesión WebSocket |
| `request_id` | uuid v4 | Cuando el evento deriva de un comando | El `requestId` del envelope cliente→servidor |
| `tick` | uint64 | Cuando el evento ocurre dentro del loop | `tickNumber` monótono |

Estos campos son el contrato. `observability` fija además los nombres de los campos frecuentes en constantes
—`unit_id`, `city_id`, `msg_type`, `error_code`— precisamente para que no convivan `player_id` y `playerId`,
que es como se pierden las búsquedas. Cualquier otro campo es específico del evento (`duration_ms`, `from`,
`to`…) y se documenta donde se emite.

La clave operativa es `request_id`: permite reconstruir la vida completa de un comando — recepción,
validación, ejecución de A\*, creación del movimiento, evento emitido y escritura persistida — con un único
filtro. Y `tick` permite correlacionar todo lo que ocurrió en el mismo tick, que es la unidad de tiempo
real del sistema.

### 1.3 Niveles: cuándo usar cada uno

Los valores que acepta `EO_LOG_LEVEL` van en minúscula (`debug`, `info`, `warn`, `error`); el campo `level`
que emite `log/slog` en cada línea va en **mayúscula** (`DEBUG`, `INFO`, `WARN`, `ERROR`). Es la diferencia
que hace que un filtro escrito a ojo no devuelva nada.

| Nivel | Uso | Ejemplos |
|---|---|---|
| `error` | El servidor no pudo cumplir su función y **alguien debe mirarlo**. Todo `error` es potencialmente una alerta. | Fallo de escritura en Postgres tras reintentos, pánico recuperado, imposibilidad de cargar el mundo al arrancar |
| `warn` | Anomalía tolerada, con degradación o riesgo. | Overrun de tick, cola de persistencia por encima del umbral, Redis no disponible con caída a modo degradado, rate limit disparado repetidamente por una conexión |
| `info` | Hechos del ciclo de vida, en volumen acotado. | `arrancando Empires Online game server`, `migraciones aplicadas`, `mundo generado y persistido` / `mundo existente cargado`, `mundo rehidratado`, `servidor de juego escuchando`, sesión creada/cerrada, transición de presencia, `apagado completado` |
| `debug` | Diagnóstico detallado, alto volumen. **No usar en producción salvo diagnóstico acotado.** | Cada comando recibido con su payload, cada búsqueda A\* con nodos expandidos, cada delta emitido |

Reglas duras:

- **Un rechazo de comando por regla de negocio no es `error`.** `TARGET_NOT_WALKABLE` o `UNIT_NOT_OWNED` son
  el sistema funcionando: se registran en `debug`, se cuentan en `eo_protocol_errors_total{code}` —no en `eo_commands_total`, que no tiene ese valor de etiqueta— y se
  desglosan por código en `eo_protocol_errors_total`. Elevarlos a `error` convierte el log en ruido y
  entrena al equipo a ignorarlo.
- **Nunca se registran secretos ni payloads sensibles.** Ni el ticket JWT, ni el DSN completo, ni el
  contenido íntegro de mensajes en producción.
- **Un log por evento, no por capa.** Registrar el mismo comando en el handler, en el servicio y en el
  repositorio triplica el volumen sin añadir información.
- **Nunca se hace logging síncrono costoso dentro del tick.** El canon prohíbe I/O bloqueante en el tick, y
  eso incluye escribir a un `stdout` bloqueado.

```json
{"ts":"2026-09-09T18:22:31.104Z","level":"INFO","service":"game-server","env":"production","msg":"movement started","player_id":"5f1c…","session_id":"s-91af","request_id":"3d6b…","tick":184220,"unit_id":4471,"from":{"x":257,"y":190},"to":{"x":268,"y":184},"waypoints":14,"arrival_time_ms":1788020551900}
{"ts":"2026-09-09T18:22:31.612Z","level":"WARN","service":"game-server","env":"production","msg":"tick overrun","tick":184225,"duration_ms":118,"budget_ms":100,"phase":"emit deltas"}
```

---

## 2. Métricas Prometheus

Expuestas en `EO_METRICS_ADDR/metrics` (por defecto `:9090/metrics`), en un listener **separado** del
público. El endpoint no es accesible desde Internet.

Los instrumentos viven en un **registro propio**, no en el global de Prometheus: así los tests construyen un
juego de métricas limpio sin colisionar entre sí. En ese registro también están los colectores estándar de Go
(`go_*`) y de proceso (`process_*`), que se obtienen gratis y conviene tener a mano en un incidente de
memoria.

**Son 14 métricas propias, y estas son exactamente sus etiquetas.** Ni más ni menos: si una consulta agrupa
por una etiqueta que no está aquí, devuelve una sola serie y engaña. Regla que gobierna toda etiqueta:
**cardinalidad acotada**. Jamás `player_id`, `unit_id`, `session_id` ni coordenadas como etiqueta: un
`player_id` en una etiqueta convierte cada jugador nuevo en una serie temporal nueva y hace estallar la base
de métricas.

### 2.1 Jugadores, conexiones y entidades (5 gauges, sin etiquetas)

| Métrica | Tipo | Etiquetas | Para qué sirve |
|---|---|---|---|
| `eo_connected_players` | Gauge | **ninguna** | Jugadores **distintos** con al menos una sesión autenticada. Es el indicador de salud del negocio y el primero que se mira ante cualquier sospecha. |
| `eo_connected_websockets` | Gauge | **ninguna** | Conexiones WebSocket abiertas. Se compara con la anterior: si `websockets` supera claramente a `players`, hay conexiones duplicadas, zombis o clientes reconectando en bucle. Que un jugador tenga varias sesiones es legítimo. |
| `eo_active_units` | Gauge | **ninguna** | Unidades vivas en la simulación. **No** está desglosada por `status`: es un único número. |
| `eo_active_movements` | Gauge | **ninguna** | Movimientos en curso. Es el mejor sustituto del desglose que no existe: correlaciona directamente con la carga del tick. |
| `eo_persistence_queue_depth` | Gauge | **ninguna** | Trabajos encolados a la espera de escribirse en PostgreSQL. **La métrica más importante después de la duración del tick.** El tick nunca hace I/O de Postgres: encola. Si crece de forma monótona, los workers no dan abasto y la ventana de pérdida ante un crash crece con ella. |

### 2.2 Game loop (2)

| Métrica | Tipo | Etiquetas | Para qué sirve |
|---|---|---|---|
| `eo_game_tick_duration_seconds` | Histogram | **ninguna** | Distribución de la duración del tick. Es **la** métrica de salud de la simulación. Buckets reales, centrados en el presupuesto de 100 ms de un tick a 10 Hz: `0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1`. Su `_count` sirve además de latido: si no crece, el loop está bloqueado. |
| `eo_game_tick_overruns_total` | Counter | **ninguna** | Ticks cuya duración superó su período (100 ms con `EO_TICK_RATE_HZ=10`). Un overrun aislado es tolerable; una tasa sostenida significa que el servidor va por detrás del mundo. |

### 2.3 Comandos y protocolo (3)

| Métrica | Tipo | Etiquetas | Para qué sirve |
|---|---|---|---|
| `eo_commands_total` | Counter | `type`, `result` | Comandos **del game loop** procesados por tipo y resultado. `type` toma los valores que devuelve `commandLabel` en `internal/game/loop`: `unit.move`, `unit.cancel_move`, `player.connected`, `player.disconnected` y `unknown`. **No incluye `session.hello`, `session.ping` ni `session.view`**: esos se resuelven en la capa WebSocket sin llegar al loop, y se cuentan en `eo_ws_messages_total`. `result` sólo toma **`accepted`** (el comando se aplicó) o **`failed`** (pánico contenido al aplicarlo). **No existe `rejected`**: un comando rechazado por regla de negocio —unidad ajena, destino intransitable— sí se aplicó desde el punto de vista del loop, y su rechazo se cuenta en `eo_protocol_errors_total{code}`. **No hay etiqueta `code`**. |
| `eo_ws_messages_total` | Counter | `direction`, `type` | Mensajes WebSocket por sentido y tipo. `direction` es **`inbound`** o **`outbound`** (no `in`/`out`); `type` es el tipo de mensaje v1. El ratio `outbound/inbound` mide la amplificación de deltas por comando, que es lo que dimensiona el ancho de banda. |
| `eo_protocol_errors_total` | Counter | `code` | Errores de protocolo emitidos, por **código estable** del catálogo de 22. Es aquí —y solo aquí— donde se responde "¿qué regla se está incumpliendo?". |

### 2.4 Pathfinding (2)

| Métrica | Tipo | Etiquetas | Para qué sirve |
|---|---|---|---|
| `eo_pathfinding_requests_total` | Counter | `result` | Consultas de pathfinding por resultado. La etiqueta admite `found` y `not_found`, pero **hoy sólo se incrementa `found`**: `Loop.observe` cuenta las consultas del tick sin distinguir su desenlace. Un A* fallido se ve únicamente en `eo_protocol_errors_total{code="PATH_NOT_FOUND"}`. Separar los dos resultados exige contarlos en `handleMoveUnit` y está **pendiente**. |
| `eo_pathfinding_duration_seconds` | Histogram | **ninguna** | Coste de cada búsqueda A\*. Se ejecuta **dentro** del tick, así que su p99 entra directamente en el presupuesto de 100 ms. Buckets reales: `0.0001, 0.00025, 0.0005, 0.001, 0.0025, 0.005, 0.01, 0.05, 0.1`. |

### 2.5 Dependencias (2)

| Métrica | Tipo | Etiquetas | Para qué sirve |
|---|---|---|---|
| `eo_database_latency_seconds` | Histogram | **ninguna** | Latencia de las operaciones contra PostgreSQL vista desde la aplicación, que es la que importa: incluye la espera por el pool. Buckets: los `DefBuckets` de Prometheus. **No** está desglosada por `operation` ni por `table`. |
| `eo_redis_latency_seconds` | Histogram | **ninguna** | **Registrada pero nunca observada.** El instrumento existe en `internal/observability/metrics.go` y aparece en `/metrics` con contadores a cero; ninguna ruta de código llama a `Observe`. Instrumentar `internal/persistence/redis` está **pendiente**. Hasta entonces la serie es plana, y una alerta sobre ella no dispararía nunca. Buckets previstos: `0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5`. |

### 2.6 Métricas que **no** existen

No las busques ni las inventes:

- **Desgloses que no están**: `eo_active_units` por `status`, `eo_commands_total` por `code`,
  `eo_database_latency_seconds` por `operation`/`table`, `eo_redis_latency_seconds` por `operation`. Añadir
  cualquiera de ellos es un cambio de código, no una consulta distinta.
- **Sistemas fuera de MVP**: combate, economía, tecnologías, comercio o clanes. No hay métricas porque no hay
  sistemas.

---

## 3. Endpoints de salud

Dos endpoints con propósitos distintos y **estrictamente separados**. Confundirlos es la causa clásica de
un bucle de reinicios que agrava un incidente en vez de resolverlo.

### 3.1 `GET /health` — liveness

| Aspecto | Definición |
|---|---|
| Pregunta que responde | ¿El proceso está vivo y capaz de responder HTTP? |
| Dependencias consultadas | **Ninguna.** No toca Postgres, ni Redis, ni el estado del loop |
| 200 | El proceso responde |
| No 200 | El proceso está muerto, colgado o sin descriptores |
| Consumidor | Supervisor de proceso (Docker / systemd) |
| Acción ante fallo | **Reiniciar el proceso** |

Es deliberadamente trivial. Si `/health` consultara Postgres, un incidente de base de datos provocaría que
el supervisor reiniciara el Game Server en bucle: perdería a todos los jugadores conectados, recargaría el
mundo una y otra vez, y añadiría carga a una base que ya está sufriendo. **Un fallo de dependencia no se
arregla reiniciando.**

```http
GET /health
200 OK
{"status":"ok"}
```

El cuerpo es deliberadamente mínimo: solo `status`. No lleva versión, commit ni *uptime* — la versión que
corre se averigua en los logs de arranque, no aquí.

### 3.2 `GET /ready` — readiness

| Aspecto | Definición |
|---|---|
| Pregunta que responde | ¿El servidor puede atender jugadores **ahora mismo**? |
| Dependencias consultadas | Postgres (ping), Redis (ping), game loop vivo (ha latido dentro del umbral) |
| 200 | Las tres comprobaciones pasan; `status: "ready"` |
| 503 | Al menos una falla; `status: "not_ready"` y el mapa `checks` dice cuál |
| Consumidor | Reverse proxy, monitorización, y la espera activa del despliegue (la imagen no declara `HEALTHCHECK`, así que `--wait` no sirve — ver [deployment.md](./deployment.md#31-imagen-docker-multi-stage)) |
| Acción ante fallo | **Diagnosticar la dependencia**, nunca reiniciar automáticamente |

Las claves del mapa `checks` son exactamente **`postgres`**, **`redis`** y **`game_loop`**. Cada dependencia
vale `"ok"` o `"error: <motivo>"`; `game_loop` vale `"ok"` o **`"stale"`**.

```http
GET /ready
503 Service Unavailable
{"status":"not_ready","checks":{"postgres":"ok","hot_state":"error: dial tcp: i/o timeout","game_loop":"ok"}}
```

El ping de cada dependencia tiene un plazo de **3 s** (contexto con timeout), de modo que una dependencia
colgada no deja `/ready` colgado también.

La comprobación del loop es la que no se puede omitir: un proceso puede responder HTTP perfectamente
mientras la goroutine del loop está bloqueada en un mutex o en un I/O que no debería estar haciendo. Desde
fuera parece sano; para el jugador, el mundo está congelado. La regla concreta: el loop late en cada tick, y
se considera **`stale`** si su último latido tiene más de **5 segundos** — 50 ticks perdidos a 10 Hz. El
umbral es generoso a propósito: filtra una pausa de GC o un pico de I/O del host, y solo se dispara ante un
bloqueo real.

Durante el apagado ordenado, `/ready` pasa a 503 **antes** de cerrar conexiones, mientras `/health` sigue en
200. Eso permite al proxy dejar de enviar conexiones nuevas mientras el servidor drena.

Ambos endpoints se sirven en `EO_HTTP_ADDR` pero **no se exponen públicamente**: el reverse proxy los
devuelve como 404 al exterior (ver [deployment.md](./deployment.md#4-reverse-proxy-y-terminación-tls)).

---

## 4. Dashboards propuestos

> **Estado: propuesta, no inventario.** Hoy **no existe ningún dashboard desplegado**, ni una instancia de
> Grafana, ni un Prometheus que raspe `/metrics`. Lo único que existe es el endpoint. Lo que sigue son los
> cuatro tableros que hay que construir, con consultas ya escritas contra las métricas y etiquetas **reales**
> de §2, para que quien los monte no tenga que inventarse nada.

La regla de diseño es que **cada tablero responda una pregunta**, no que acumule paneles.

### 4.1 Salud del loop — "¿el mundo va a tiempo?"

| Panel | Consulta | Lectura |
|---|---|---|
| Duración del tick p50 / p95 / p99 | `histogram_quantile(0.99, sum(rate(eo_game_tick_duration_seconds_bucket[5m])) by (le))` | Línea de referencia en 0.1 s (el presupuesto). El p99 debe vivir muy por debajo |
| Tasa de overruns | `rate(eo_game_tick_overruns_total[5m])` | Ticks/s que pasaron de presupuesto. Con 10 Hz, 0.1/s es un 1 % |
| Latido del loop | `rate(eo_game_tick_duration_seconds_count[1m])` | Debe ser ≈ `EO_TICK_RATE_HZ` (10). Si cae, el loop se está saltando ticks; si es 0, está muerto |
| Unidades y movimientos activos | `eo_active_units` y `eo_active_movements` superpuestos | `eo_active_movements` es el principal impulsor del coste del tick. **No hay desglose por `status`** (§2.6) |
| Duración de A\* p99 | `histogram_quantile(0.99, sum(rate(eo_pathfinding_duration_seconds_bucket[5m])) by (le))` | Entra dentro del presupuesto del tick |
| Resultado de A\* | `sum(rate(eo_pathfinding_requests_total[5m])) by (result)` | Solo `found` / `not_found`. Un `not_found` creciente = problema de mundo o de cliente |

### 4.2 Red y protocolo — "¿qué están enviando los clientes?"

| Panel | Consulta | Lectura |
|---|---|---|
| Mensajes por segundo por sentido | `sum(rate(eo_ws_messages_total[1m])) by (direction)` | Amplificación `outbound/inbound`: dimensiona el ancho de banda |
| Top de tipos entrantes | `topk(5, sum(rate(eo_ws_messages_total{direction="inbound"}[5m])) by (type))` | Un `session.view` dominante indica un cliente que arrastra vista sin *throttle* |
| Tasa de `system.error` | `sum(rate(eo_ws_messages_total{direction="outbound",type="system.error"}[5m]))` | Tasa absoluta de error del protocolo |
| Comandos por resultado | `sum(rate(eo_commands_total[5m])) by (result)` | Proporción `accepted` / `rejected` / `failed` |
| Errores por código | `topk(8, sum(rate(eo_protocol_errors_total[5m])) by (code))` | Identifica **qué** regla se está incumpliendo. Va contra `eo_protocol_errors_total`, no contra `eo_commands_total`, que no tiene etiqueta `code` |

### 4.3 Persistencia — "¿estamos perdiendo el hilo con la base de datos?"

| Panel | Consulta | Lectura |
|---|---|---|
| Profundidad de la cola | `eo_persistence_queue_depth` | **La pendiente importa más que el valor.** Una cola que sube sin bajar nunca se recupera sola |
| Tendencia de la cola | `deriv(eo_persistence_queue_depth[10m])` | Positiva y sostenida = los workers no dan abasto |
| Latencia de Postgres p95 / p99 | `histogram_quantile(0.99, sum(rate(eo_database_latency_seconds_bucket[5m])) by (le))` | Latencia agregada, incluida la espera por el pool. **Sin desglose por `operation` ni por `table`** (§2.6) |
| Volumen de operaciones | `rate(eo_database_latency_seconds_count[5m])` | Operaciones por segundo contra Postgres; un salto sin más jugadores es sospechoso |
| Latencia de Redis p99 | `histogram_quantile(0.99, sum(rate(eo_redis_latency_seconds_bucket[5m])) by (le))` | Afecta a presencia y a los logins |

### 4.4 Jugadores — "¿hay alguien y está bien?"

| Panel | Consulta | Lectura |
|---|---|---|
| Jugadores conectados | `eo_connected_players` | Serie principal; compárala con la misma hora de días anteriores |
| WebSockets frente a jugadores | `eo_connected_websockets` y `eo_connected_players` superpuestos | Divergencia grande = conexiones zombis o reconexión en bucle. Alguna divergencia es normal: un jugador puede tener varias sesiones |
| Variación en 5 min | `delta(eo_connected_players[5m])` | Caídas bruscas: ver alerta §5.7 |
| Tasa de handshakes | `rate(eo_ws_messages_total{direction="inbound",type="session.hello"}[5m])` | Un pico sin subida de `eo_connected_players` = los handshakes están fallando |

---

## 5. Reglas de alerta propuestas

> **Estado: propuesta, no configuración desplegada.** No hay Alertmanager, ni destinatarios, ni ninguna de
> estas reglas cargada en ninguna parte. El YAML que sigue está escrito para copiarse tal cual el día que se
> monte el stack, y ese día hay que probar cada regla disparándola a mano antes de darla por buena.

Cada regla lleva su justificación. **Un umbral sin justificación se ignora tras el tercer falso positivo**,
y una alerta ignorada es peor que no tenerla. Todos los cálculos suponen `EO_TICK_RATE_HZ=10` (período
100 ms); si esa variable cambia, hay que recalcular §5.1 y §5.2.

### 5.1 Overruns de tick sostenidos

```yaml
- alert: EOTickOverrunsSustained
  expr: rate(eo_game_tick_overruns_total[5m]) > 0.5
  for: 10m
  labels: { severity: warning }
  annotations:
    summary: "Más del 5% de los ticks exceden su presupuesto de 100 ms"

- alert: EOTickOverrunsCritical
  expr: rate(eo_game_tick_overruns_total[5m]) > 2
  for: 5m
  labels: { severity: critical }
  annotations:
    summary: "Más del 20% de los ticks exceden su presupuesto: la simulación va por detrás del tiempo real"
```

**Justificación.** A 10 Hz hay 10 ticks por segundo. Un umbral de 0.5 overruns/s equivale al **5 %** de los
ticks; 2/s equivale al **20 %**. Un overrun suelto (GC, pico de I/O del host) es normal. El 5 % sostenido
durante 10 minutos significa que la carga ya no cabe en el presupuesto y que las llegadas de movimiento se
retrasan de forma acumulativa: el jugador percibe un mundo con tirones. El 20 % es la antesala de que el
loop no pueda recuperar el retraso.

### 5.2 p99 de duración de tick

```yaml
- alert: EOTickP99High
  expr: histogram_quantile(0.99, sum(rate(eo_game_tick_duration_seconds_bucket[5m])) by (le)) > 0.05
  for: 10m
  labels: { severity: warning }
  annotations:
    summary: "El p99 del tick supera el 50% del presupuesto (50 ms de 100 ms)"

- alert: EOTickP99Critical
  expr: histogram_quantile(0.99, sum(rate(eo_game_tick_duration_seconds_bucket[5m])) by (le)) > 0.09
  for: 5m
  labels: { severity: critical }
```

**Justificación.** Es la alerta *anticipatoria*: los overruns son el síntoma, el p99 es la tendencia. Con el
p99 en 50 ms queda la mitad del presupuesto de margen para un pico de jugadores o de unidades en
movimiento; con el p99 en 90 ms, cualquier variación produce overruns inmediatamente. Actuar en el aviso
evita tener que actuar en la crisis.

### 5.3 Profundidad de la cola de persistencia

```yaml
- alert: EOPersistenceQueueGrowing
  expr: eo_persistence_queue_depth > 1000 and deriv(eo_persistence_queue_depth[10m]) > 0
  for: 10m
  labels: { severity: warning }
  annotations:
    summary: "La cola de persistencia crece de forma sostenida: los workers no drenan"

- alert: EOPersistenceQueueCritical
  expr: eo_persistence_queue_depth > 10000
  for: 5m
  labels: { severity: critical }
  annotations:
    summary: "Cola de persistencia crítica: riesgo real de pérdida de estado ante un crash"
```

**Justificación.** La cola es un amortiguador entre el loop (que nunca hace I/O de Postgres) y los workers.
Que suba en un pico es su función. Que suba **y no baje** significa que la tasa de generación supera a la
de drenaje, algo que no se corrige solo: la cola crecerá hasta agotar memoria. Además, todo lo encolado y
no escrito se pierde en un crash, así que la profundidad es literalmente el tamaño de la ventana de
pérdida. La condición `deriv > 0` elimina los falsos positivos de los picos que sí se recuperan.

### 5.4 Latencia de base de datos

```yaml
- alert: EODatabaseLatencyHigh
  expr: histogram_quantile(0.99, sum(rate(eo_database_latency_seconds_bucket[5m])) by (le)) > 0.05
  for: 10m
  labels: { severity: warning }

- alert: EODatabaseLatencyCritical
  expr: histogram_quantile(0.99, sum(rate(eo_database_latency_seconds_bucket[5m])) by (le)) > 0.25
  for: 5m
  labels: { severity: critical }

- alert: EORedisLatencyHigh
  expr: histogram_quantile(0.99, sum(rate(eo_redis_latency_seconds_bucket[5m])) by (le)) > 0.02
  for: 10m
  labels: { severity: warning }
```

**Justificación.** Contra una base gestionada en red privada, una escritura sencilla debería estar en el
orden del milisegundo. Un p99 de 50 ms es diez veces peor que lo normal y es el precursor directo del
crecimiento de la cola (§5.3): esta alerta suele dispararse **antes** y explica por qué. 250 ms significa
que las escrituras encoladas — inicio y fin de movimiento, transiciones de presencia — están tardando
lo bastante como para afectar a la experiencia. En Redis el umbral es más estricto (20 ms) porque está en
la ruta del handshake, donde consumir el `jti` es bloqueante para el login.

Nota sobre los *buckets*: `eo_database_latency_seconds` usa los `DefBuckets` de Prometheus, cuyo primer
corte está en 5 ms. Por debajo de eso el cuantil no distingue nada, así que estos umbrales (50 ms, 250 ms)
son los que la métrica puede sostener; una alerta a 2 ms sería ruido de interpolación, no una señal.

### 5.5 Caída de readiness

```yaml
- alert: EOReadinessDown
  expr: probe_success{job="eo-ready"} == 0
  for: 1m
  labels: { severity: critical }
  annotations:
    summary: "/ready no responde 200: el servidor no puede atender jugadores"

- alert: EOLoopStalled
  expr: rate(eo_game_tick_duration_seconds_count[2m]) < 1
  for: 2m
  labels: { severity: critical }
  annotations:
    summary: "El game loop no avanza: menos de 1 tick/s frente a los 10 esperados"
```

**Justificación.** `for: 1m` filtra el reinicio normal de un despliegue (ventana de 15–60 s, ver
[deployment.md](./deployment.md#85-ventana-de-indisponibilidad-esperada)) sin dejar pasar una caída real.
`EOLoopStalled` es la red de seguridad para el caso peor: el proceso responde HTTP pero el mundo está
congelado. Con 10 Hz esperados, menos de 1 tick/s no admite explicación benigna.

### 5.6 Tasa de errores del protocolo

```yaml
- alert: EOProtocolErrorRateHigh
  expr: |
    sum(rate(eo_ws_messages_total{direction="outbound",type="system.error"}[5m]))
      /
    clamp_min(sum(rate(eo_ws_messages_total{direction="inbound"}[5m])), 1)
      > 0.05
  for: 10m
  labels: { severity: warning }
  annotations:
    summary: "Más del 5% de los mensajes entrantes producen system.error"

- alert: EOInternalErrorsRising
  expr: sum(rate(eo_protocol_errors_total{code="INTERNAL_ERROR"}[5m])) > 0.1
  for: 5m
  labels: { severity: critical }
  annotations:
    summary: "INTERNAL_ERROR sostenido: bug del servidor, no error de cliente"
```

**Justificación.** Un cliente correcto casi nunca produce errores: pide movimientos válidos porque la UI
solo ofrece destinos válidos. Un 5 % de errores sostenido significa un cliente desplegado con un bug, una
divergencia de esquema entre `packages/protocol` y el servidor, o alguien sondeando el protocolo.
`INTERNAL_ERROR` merece regla propia y severidad crítica porque, a diferencia de todos los demás códigos, no
lo causa el cliente: **lo causa el servidor**. 0.1/s (6 por minuto) es señal inequívoca de un bug activo.

### 5.7 Caída brusca de jugadores conectados

```yaml
- alert: EOPlayersDroppedSharply
  expr: |
    delta(eo_connected_players[5m]) < -10
      and
    eo_connected_players < 0.5 * (eo_connected_players offset 10m)
  for: 5m
  labels: { severity: warning }
  annotations:
    summary: "Los jugadores conectados cayeron más de un 50% en 5 minutos"

- alert: EOPlayersZeroUnexpected
  expr: eo_connected_players == 0 and (eo_connected_players offset 30m) > 20
  for: 10m
  labels: { severity: critical }
  annotations:
    summary: "Cero jugadores conectados donde hace media hora había más de veinte"
```

**Justificación.** Es la única alerta que mide **el efecto real sobre las personas**, no una causa técnica.
Detecta lo que el resto puede no ver: un fallo de TLS que impide conectar, un `EO_AUTH_JWT_SECRET`
desincronizado tras una rotación mal hecha, una caída del frontend, o un problema de red aguas arriba. Las
dos condiciones combinadas (caída absoluta > 10 **y** relativa > 50 %) evitan que salte con el descenso
natural de la madrugada, cuando bajar de 8 a 3 jugadores es normal. `EOPlayersZeroUnexpected` cubre el caso
extremo con contexto histórico.

---

## 6. Runbook: qué mirar primero

Diagnóstico dirigido. La columna de la izquierda es lo que se observa; la secuencia es el orden en que hay
que mirar, no una lista de sugerencias.

### 6.1 "El juego va a tirones" / overruns o p99 alto

1. **`eo_active_movements`**, `eo_active_units` y `eo_connected_players`. ¿Es simplemente más carga? Si han
   crecido proporcionalmente, es un problema de capacidad, no un bug.
2. **`eo_pathfinding_duration_seconds` p99** y `eo_pathfinding_requests_total{result="not_found"}`. El A\*
   corre dentro del tick: si su p99 se ha disparado, es el sospechoso principal. Cruza con
   `eo_protocol_errors_total{code="PATH_TOO_LONG"}` para ver si alguien está pidiendo rutas al límite de
   `EO_PATHFINDING_MAX_DISTANCE` (256) o rutas que agotan `EO_PATHFINDING_MAX_NODES` (20000) antes de fallar.
3. **`eo_persistence_queue_depth`.** Si la cola también crece, el problema probablemente **no** está en el
   tick sino aguas abajo, y la presión de memoria está afectando a todo.
4. **`eo_database_latency_seconds`.** Si está alta **y** el tick se resiente, sospecha lo peor: **I/O
   síncrono de Postgres dentro del tick**, que el canon prohíbe expresamente y que el diseño evita encolando
   toda escritura. Es un bug de diseño, no de capacidad.
5. Logs `"level":"WARN","msg":"tick overrun"` con su campo `phase`: dicen exactamente qué fase se pasó de
   presupuesto.

### 6.2 "La cola de persistencia crece"

1. **`eo_database_latency_seconds`** (p99 y `_count`). Casi siempre la causa está aquí. No hay desglose por
   `operation`: si necesitas saber *qué* operación va lenta, hay que ir a los logs o al panel del proveedor.
2. Estado del Postgres gestionado: CPU, IOPS, conexiones activas, espera por bloqueos.
3. **`eo_active_units` y `eo_active_movements`.** ¿Hay muchas más entidades generando estado *dirty* del habitual?
4. `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS` (50): un valor **más bajo** produce lotes más pequeños y más
   frecuentes; puede empeorar la situación si el cuello de botella es el número de transacciones, no su
   tamaño.
5. Si la cola no se recupera, es un incidente: ver
   [disaster-recovery.md](./disaster-recovery.md#e3-postgres-caído-sin-corrupción).

### 6.3 "`/ready` devuelve 503"

1. **Lee el cuerpo de `/ready`**: el mapa `checks` dice qué comprobación falla — `postgres`, `redis` o
   `game_loop`. No adivines.
2. Si falla `postgres` o `redis`: es un problema de dependencia o de red. **No reinicies el Game Server**,
   no arregla nada y desconecta a todos.
3. Si `game_loop` está en **`stale`**: el proceso vive pero la simulación lleva más de 5 s sin latir. Captura
   un volcado de goroutines antes de hacer nada — es la única evidencia que permitirá encontrar el
   bloqueo — y luego reinicia.
4. Si `/health` también falla: el proceso está muerto o colgado; el supervisor debería estar reiniciándolo.
   Ver [disaster-recovery.md](./disaster-recovery.md#e1-crash-del-game-server).

### 6.4 "Los jugadores no pueden conectar"

1. **`rate(eo_ws_messages_total{direction="inbound",type="session.hello"}[5m])`.** ¿Llegan handshakes?
   - **No llegan** → el problema está delante del servidor: DNS, TLS, reverse proxy, firewall, o el
     frontend no está emitiendo tickets. Prueba `curl -I https://game.<dominio>/`.
   - **Llegan pero `eo_connected_players` no sube** → el handshake falla. Sigue en el punto 2.
2. Logs de cierre por código: `4401` (secreto desincronizado o ticket expirado — sospecha de una rotación
   mal ejecutada, ver [configuration.md](./configuration.md#63-rotación-de-eo_auth_jwt_secret)), `4408`
   (el cliente no envía `session.hello` dentro del `WSHandshakeTimeout` de 5 s), `4429` (rate limit), `4400`
   (mensaje inválido: sospecha divergencia de esquema del protocolo), `4500` (cola de salida de la sesión
   llena; ver `EO_WS_OUTBOUND_QUEUE_SIZE`).
3. **`eo_redis_latency_seconds`.** El consumo del `jti` en `ticket:jti:{jti}` es parte del handshake: si
   Redis está lento o caído, los logins fallan aunque todo lo demás esté bien.
4. Comprueba que emisor y verificador comparten `EO_AUTH_JWT_SECRET`. El servidor **no** registra una huella
   del secreto (ver [configuration.md](./configuration.md#53-registro-de-la-configuración-efectiva)), así que
   la verificación práctica es intentar una conexión real con un ticket recién emitido.

### 6.5 "Sube la tasa de `system.error`"

1. **`topk(8, sum(rate(eo_protocol_errors_total[5m])) by (code))`.** El código dice qué pasa. (Es esta
   métrica y no `eo_commands_total`: la de comandos solo tiene `type` y `result`.)
   - `INVALID_MESSAGE` / `UNSUPPORTED_VERSION` → divergencia entre el esquema del cliente y el del servidor.
     Recuerda que los esquemas cliente→servidor son **estrictos** (`additionalProperties: false`): un campo
     de más basta para el rechazo. Comprueba qué versión del frontend está desplegada frente a la del Game
     Server, y si el espejo embebido en Go está al día (`pnpm run protocol:check`).
   - `RATE_LIMITED` → un cliente en bucle, o abuso.
   - `UNIT_NOT_OWNED` / `FORBIDDEN` → bug del cliente o intento de manipulación. Correlaciona por
     `player_id` en los logs.
   - `PATH_NOT_FOUND` / `TARGET_NOT_WALKABLE` / `PATH_TOO_LONG` → la UI está ofreciendo destinos que el
     servidor rechaza: posible desincronización de la capa de ocupación en el cliente.
   - `INTERNAL_ERROR` → **bug del servidor**, o contrapresión: la cola de comandos llena descarta el comando
     y responde con este código. Cruza con `eo_persistence_queue_depth` antes de escalar.
2. Filtra los logs por el código y extrae los `request_id` afectados; con uno solo se reconstruye el caso
   completo.

### 6.6 "Caída brusca de jugadores conectados"

1. `eo_connected_websockets` frente a `eo_connected_players`: ¿se cayeron las conexiones, o solo se
   desautenticaron?
2. ¿Hubo un despliegue en la última hora? Correlaciona con el registro de despliegues. La sospecha por
   defecto tras un despliegue es el despliegue.
3. `/ready` y las alertas del loop: si el servidor está sano, el problema está aguas arriba (proxy, TLS,
   red, frontend).
4. Prueba una conexión real desde fuera, no desde el VPS. Buena parte de los fallos de este tipo solo se ven
   desde Internet.
5. Si el servidor está sano y las conexiones no vuelven: ver
   [disaster-recovery.md](./disaster-recovery.md#e6-partición-de-red).

---

## Referencias

- [deployment.md](./deployment.md) — verificación post-despliegue y exposición de `/metrics`.
- [configuration.md](./configuration.md) — `EO_LOG_LEVEL`, `EO_METRICS_ADDR`, `EO_TICK_RATE_HZ`.
- [disaster-recovery.md](./disaster-recovery.md) — escalado cuando el runbook no basta.
- [backups.md](./backups.md) — qué vigilar en el ciclo de backups.
- [../architecture/game-loop.md](../architecture/game-loop.md) — fases del tick y presupuesto.
- [../architecture/persistence.md](../architecture/persistence.md) — cola de persistencia y write-through.
- [../specs/websocket-protocol.md](../specs/websocket-protocol.md) — tipos de mensaje y catálogo de 22 códigos de error.

# ADR-004: Redis como hot state

Propósito: delimitar qué estado vive en Redis, qué no puede vivir en Redis bajo ninguna circunstancia, y garantizar que la pérdida total de Redis nunca implique pérdida de estado durable.

- **Estado:** Aceptado
- **Fecha:** 2026-09-09
- **Decisores:** equipo de arquitectura
- **Relacionados:** [ADR-002](ADR-002-authoritative-server.md), [ADR-003](ADR-003-postgresql-source-of-truth.md), [ADR-006](ADR-006-websocket-protocol.md)

---

## Contexto

Hay estado en Empires Online que no encaja bien ni en PostgreSQL ni en la RAM del proceso. Tiene tres rasgos comunes: **caduca solo**, **se consulta o se escribe con mucha frecuencia** y **debe ser visible desde más de un proceso**.

Los casos concretos del MVP:

1. **Presencia del jugador.** Clave `presence:player:{playerId}` con TTL de 30 s (`EO_PRESENCE_TTL_SECONDS`), refrescada por heartbeat cada 10 s (`EO_PRESENCE_HEARTBEAT_SECONDS`). La transición de una ciudad a `OFFLINE_PENDING` ocurre cuando no hay conexión WS activa **y** además la clave de presencia ha expirado; ese solapamiento es justamente el *grace* de reconexión. La expiración por tiempo es la semántica del dato, no un detalle de implementación.
2. **Consumo de tickets de autenticación.** El game ticket es un JWT HS256 con TTL de 60 s y claim `jti`. Para impedir el replay, el `jti` se consume una sola vez: `ticket:jti:{jti}` con TTL de 120 s. Necesita ser atómico ("regístralo si no existe") y necesita caducar solo.
3. **Idempotencia de comandos.** Cada comando lleva `requestId` (UUIDv4). El servidor lo registra en `idem:{playerId}:{requestId}` con TTL de 300 s; un `requestId` repetido devuelve la respuesta original y no re-ejecuta. Para comandos durables se registra **además** en la tabla `idempotency_keys` de PostgreSQL.
4. **Sesiones, locks y cooldowns.** Estado vivo asociado a una conexión o a una acción, con vida corta y necesidad de coordinación.
5. **Caché de lecturas calientes.** Datos derivados de PostgreSQL que se consultan constantemente y cambian poco.

Ninguno de estos casos se resuelve bien con las otras dos capas:

- **PostgreSQL** no tiene expiración por tiempo. Emular un TTL exige una columna `expires_at`, filtrarla en cada lectura y un proceso de barrido que borre filas muertas. Además, un heartbeat cada 10 s por jugador conectado es una escritura recurrente sobre la fuente de verdad durable a cambio de un dato que no vale nada dentro de 30 segundos. Es tráfico de escritura caro sobre el recurso más difícil de escalar ([ADR-003](ADR-003-postgresql-source-of-truth.md)).
- **La RAM del proceso** es la opción más rápida y sin dependencias, pero es privada: dos procesos de game server no verían la misma presencia, no podrían coordinar un lock, y un ticket consumido en uno sería reutilizable en el otro. Además desaparece en cada despliegue, y con ella el *grace* de reconexión de todos los jugadores conectados.

El canon fija la jerarquía sin ambigüedad: PostgreSQL es la fuente de verdad durable, Redis es *hot/transient state*, y **Redis nunca sustituye a PostgreSQL**.

## Decisión

**Redis se adopta como capa de estado caliente y transitorio.** Se conecta mediante `EO_REDIS_URL` desde `internal/persistence/redis`.

### Qué vive en Redis

| Categoría | Clave canónica | TTL | Por qué aquí |
|---|---|---|---|
| Presencia del jugador | `presence:player:{playerId}` | 30 s (`EO_PRESENCE_TTL_SECONDS`) | La expiración *es* la semántica; heartbeat cada 10 s |
| Consumo de ticket (anti-replay) | `ticket:jti:{jti}` | 120 s | Escritura condicional atómica y caducidad automática |
| Idempotencia de comandos | `idem:{playerId}:{requestId}` | 300 s | Lectura en la ruta caliente de cada comando |
| Estado vivo de sesión | *namespace TBD* | corto | Coordinación entre conexiones y procesos |
| Locks de coordinación | *namespace TBD* | corto | Exclusión mutua entre procesos, con expiración obligatoria |
| Cooldowns | *namespace TBD* | según regla | Caducan solos; reconstruibles o no críticos |
| Caché de lecturas calientes | *namespace TBD* | corto | Derivado de PostgreSQL, siempre reconstruible |

El canon fija literalmente los tres primeros patrones de clave y sus TTL. Los prefijos exactos de los namespaces de sesión, locks, cooldowns y caché son **TBD (fuera de MVP)** y se documentarán en [../architecture/persistence.md](../architecture/persistence.md) cuando se implementen; el criterio que ya está fijado es que **toda clave lleva TTL** y que ninguna de ellas contiene estado durable.

### Qué NUNCA va en Redis

Regla dura, sin excepciones ni casos especiales:

- **Ningún estado durable.** Nada de lo que vive en `players`, `cities`, `units`, `unit_movements`, `world_state`, `world_chunks`, `territories`, `territory_control`, `safe_zones`, `treaties`, `garrisons` ni `world_events` tiene su copia autoritativa en Redis.
- **Ninguna posición autoritativa de unidad.** La posición se deriva de la polilínea temporizada persistida en `unit_movements` ([ADR-011](ADR-011-movement-timed-polyline.md)). Cachearla sería crear una segunda verdad.
- **Ningún resultado de simulación.** Ownership, HP, recursos, resultados de combate (fuera de MVP): todos ellos son estado autoritativo del servidor y durable en PostgreSQL ([ADR-002](ADR-002-authoritative-server.md)).
- **Ningún registro de idempotencia de un comando con efectos durables como única copia.** El registro en Redis es el camino rápido; `idempotency_keys` en PostgreSQL es la garantía.
- **Ningún secreto ni credencial.** Los secretos van por variables de entorno (`EO_AUTH_JWT_SECRET`, `EO_POSTGRES_URL`, `EO_REDIS_URL`), jamás al repositorio y jamás al cliente.
- **Ninguna clave sin TTL.** Una clave permanente en Redis es, por definición, estado durable mal colocado. Si algo debe existir indefinidamente, su sitio es PostgreSQL.

### El criterio de decisión, en una pregunta

> Si Redis se borrase entero ahora mismo, ¿se perdería algo que un jugador consideraría suyo?

Si la respuesta es sí, el dato está mal colocado. Ese es el test que debe pasar cualquier clave nueva antes de añadirse, y forma parte de la revisión de toda PR que toque `internal/persistence/redis`.

```
        ┌──────────────────────────────────────────────┐
        │  RAM del Game Server                         │
        │  Simulación activa · privada del proceso     │
        │  Se pierde al reiniciar → se reconstruye     │
        └──────────────────┬───────────────────────────┘
                           │
        ┌──────────────────▼───────────────────────────┐
        │  Redis — HOT STATE                           │
        │  Presencia · sesiones · locks · cooldowns    │
        │  Idempotencia (camino rápido) · caché        │
        │  TODO con TTL · compartido entre procesos    │
        │  Se pierde → degradación, NUNCA pérdida      │
        └──────────────────┬───────────────────────────┘
                           │
        ┌──────────────────▼───────────────────────────┐
        │  PostgreSQL — DURABLE SOURCE OF TRUTH        │
        │  Todo lo que un jugador considera suyo       │
        │  Nunca se deriva de Redis                    │
        └──────────────────────────────────────────────┘
```

La flecha de reconstrucción va **siempre hacia arriba**: RAM se reconstruye desde PostgreSQL, y Redis se repuebla desde el comportamiento vivo del sistema (nuevos heartbeats, nuevas sesiones). Nunca hacia abajo.

## Alternativas consideradas

### A. Solo memoria del proceso (sin Redis)

**Ventajas reales, y no son pocas.** Es la opción más rápida en términos absolutos: acceso a un `map` en Go frente a un round-trip de red, un orden de magnitud de diferencia por operación. Elimina una dependencia entera del despliegue, del `docker-compose.yml`, del `GET /ready` y de la matriz de fallos. Menos configuración (`EO_REDIS_URL` desaparecería), menos código de serialización, menos superficie que operar y menos que aprender para alguien nuevo. Para un MVP con un solo proceso es, honestamente, suficiente.

**Por qué se descarta.**

- **Impide la coordinación multi-proceso.** Es el motivo principal. Con dos instancias del game server —lo que ocurrirá tarde o temprano, aunque sea durante un despliegue con solapamiento— la presencia sería inconsistente, un `jti` consumido en la instancia A seguiría siendo válido en la B (agujero de replay directo), y no habría forma de tomar un lock global.
- **Un despliegue borra la presencia de todos.** Al reiniciar, ninguna clave `presence:player:{playerId}` existe, así que todas las ciudades de jugadores conectados avanzarían hacia `OFFLINE_PENDING` en cuanto se evaluara la condición, a pesar de que los jugadores solo han sufrido un corte de segundos. El *grace* de reconexión desaparecería justo cuando más falta hace.
- **La idempotencia se rompería en el reinicio**, que es precisamente cuando el cliente reintenta comandos.
- Un mapa en memoria con TTL hay que implementarlo y barrerlo a mano, con su propia goroutine y sus propios bugs.

La opción se rechaza por su techo estructural, no por rendimiento.

**Adoptada después, y sólo bajo guarda, para desarrollo.** Los cuatro motivos de arriba son razones
para no depender de memoria en **producción**; ninguno aplica a una única instancia en la máquina de
un desarrollador. Y el coste de exigir Redis ahí resultó real: en Windows, tanto Docker Desktop como
Memurai necesitan elevación para instalar o arrancar su servicio, lo que bloqueaba por completo poder
ejecutar el juego.

Así que existe `internal/persistence/memory`, con la misma superficie que el paquete `redis`, y
`EO_REDIS_URL` pasa a ser opcional: vacía conmuta a memoria. La guarda es lo que mantiene esta ADR
intacta — **`internal/config` falla el arranque si `EO_ENV=production` y `EO_REDIS_URL` está vacía**,
y fuera de producción el servidor emite un `WARN` ruidoso enumerando lo que se pierde. La decisión
sigue siendo Redis; lo que se añadió es una alternativa acotada y bloqueada donde importaría.

Ventaja lateral que no se anticipó: sus tests usan un reloj falso, así que verifican los TTL de forma
instantánea y determinista, cosa que los tests contra Redis real no pueden hacer sin `sleep`.

### B. Solo PostgreSQL (sin Redis)

**Ventajas reales.** Una dependencia menos que operar, un único modelo mental de consistencia, y presencia e idempotencia participando de las mismas transacciones que el resto del estado — lo que elimina cualquier ventana de incoherencia entre almacenes. También simplifica el `docker-compose.yml`, el CI y el `GET /ready`. Y para el volumen del MVP, PostgreSQL aguantaría la carga sin despeinarse.

**Por qué se descarta.**

- **Castiga las lecturas y escrituras calientes.** Un heartbeat cada 10 s por jugador conectado es una escritura periódica contra el recurso más caro y menos escalable del sistema, a cambio de un dato de 30 segundos de vida. Con miles de conectados, es tráfico constante que compite con las escrituras que sí importan.
- **No hay expiración por TTL.** Habría que añadir `expires_at` a cada tabla afectada, filtrarla en toda lectura y ejecutar un barrido periódico. Ese barrido es código nuevo, con su ventana de retraso y su propio modo de fallo.
- **La comprobación de idempotencia entra en la ruta caliente de cada comando**, añadiendo una consulta a PostgreSQL por comando recibido; con rate limit de 20 msg/s por conexión, es carga que no aporta durabilidad.
- Los locks se emularían con `SELECT ... FOR UPDATE` o *advisory locks*, manteniendo transacciones abiertas mientras se simula: exactamente el patrón que [ADR-003](ADR-003-postgresql-source-of-truth.md) prohíbe.

### C. Memcached

**Ventajas reales.** Más simple que Redis, muy rápido, excelente como caché pura y con un modelo de memoria muy predecible.

**Por qué se descarta.** No tiene las primitivas que aquí importan: escritura condicional atómica con semántica clara para el consumo de `jti`, estructuras de datos ricas para sesiones y cooldowns, ni pub/sub para una eventual coordinación entre procesos. Es una caché, y aquí se necesita algo más que una caché.

### D. Comparativa

| Criterio | Redis | RAM del proceso | PostgreSQL | Memcached |
|---|---|---|---|---|
| Latencia por operación | Muy baja | Mínima | Media | Muy baja |
| TTL nativo | Sí | Manual | No | Sí |
| Visible entre procesos | Sí | **No** | Sí | Sí |
| Sobrevive a un reinicio del server | Sí | **No** | Sí | Sí |
| Operaciones atómicas condicionales | Sí | Sí (in-process) | Sí | Limitadas |
| Estructuras de datos ricas | Sí | Sí | Sí | No |
| Dependencias operativas añadidas | +1 | 0 | 0 | +1 |
| Apto como almacén durable | **No** | No | Sí | No |

## Consecuencias

### Positivas

- **La presencia sobrevive a un reinicio del game server.** Un despliegue no dispara transiciones falsas a `OFFLINE_PENDING`, porque las claves con TTL siguen vivas en Redis mientras el proceso reinicia.
- **El anti-replay de tickets es correcto también con varias instancias**, lo que mantiene válida la garantía de [../architecture/overview.md](../architecture/overview.md) durante despliegues con solapamiento.
- **Se descarga a PostgreSQL de escrituras de alta frecuencia y bajo valor**, preservando su capacidad de escritura para el estado que sí importa.
- **La idempotencia tiene un camino rápido** en la ruta de cada comando, sin renunciar a la garantía durable en `idempotency_keys` para los comandos con efectos persistentes.
- **La expiración es declarativa.** No hay procesos de barrido que escribir, probar ni vigilar: el TTL forma parte de la escritura.
- Queda abierta la puerta a escalar a varias instancias del game server sin rediseñar la capa de presencia ni la de autenticación.

### Negativas

- **Una dependencia operativa más que puede caer.** Es el coste principal y obliga a documentar y probar un modo degradado explícito (ver más abajo). Aumenta la matriz de fallos: ahora hay que razonar sobre "PostgreSQL bien y Redis mal", que antes no existía.
- **Dos almacenes pueden desincronizarse.** Un registro de idempotencia escrito en Redis pero no en `idempotency_keys` (o al revés) deja una ventana de incoherencia. Se acota haciendo de PostgreSQL la garantía y de Redis la optimización, nunca al contrario, pero la ventana existe.
- **Latencia de red en la ruta caliente.** Cada comprobación de idempotencia y cada verificación de `jti` cruzan la red. Se vigila con `eo_redis_latency_seconds`; si Redis se vuelve lento, el efecto se nota en el tiempo de proceso de cada comando.
- **`GET /ready` incluye Redis**, así que una indisponibilidad de Redis saca al proceso de rotación. Es correcto, pero significa que Redis participa de la disponibilidad del servicio.
- **Riesgo de deriva de alcance.** Redis es cómodo y rápido, y la tentación de "cachear una cosita más" es constante. Sin la regla dura de la sección "Qué NUNCA va en Redis", el sistema termina con dos fuentes de verdad y con bugs imposibles de reproducir. Esta consecuencia es de disciplina, y la disciplina se erosiona.
- **Coste en desarrollo local.** `redis-cli` no está instalado en la máquina; la inspección se hace vía `docker compose exec`, y los tests de integración exigen Docker Desktop iniciado con `EO_INTEGRATION=1`.

### Neutras

- El `docker-compose.yml` incluye un servicio Redis junto al de PostgreSQL, y el pipeline de CI lo arranca para la etapa de integración.
- Se añade `EO_REDIS_URL` a la configuración, gestionada como el resto por `internal/config`.
- Se añade la métrica `eo_redis_latency_seconds` al conjunto expuesto en `EO_METRICS_ADDR/metrics`.
- La política de memoria y de desalojo de Redis en producción (`maxmemory`, política de eviction) es **TBD (fuera de MVP)**. Nota importante para cuando se decida: como toda clave lleva TTL y ninguna contiene estado durable, un desalojo es tolerable por diseño, pero el desalojo de `ticket:jti:{jti}` antes de su expiración reabriría una ventana de replay de hasta 60 s (el TTL del ticket), y esa consideración debe entrar en la decisión.

## Modo degradado: pérdida total de Redis

**Regla inviolable: la pérdida total de Redis no pierde estado durable.** Todo lo que un jugador considera suyo está en PostgreSQL. Redis vacío significa servicio degradado, jamás mundo dañado.

Comportamiento esperado ante un Redis caído o vaciado:

| Función | Efecto de la pérdida | Respuesta del sistema |
|---|---|---|
| `GET /ready` | Deja de estar listo (readiness comprueba PostgreSQL + Redis + loop vivo) | El proceso sale de rotación; `GET /health` sigue respondiendo porque la liveness no depende de dependencias externas |
| Presencia | Todas las claves `presence:player:{playerId}` desaparecen | Las conexiones WS vivas siguen siendo conexiones vivas: la condición para `OFFLINE_PENDING` exige **ausencia de conexión WS activa además de** expiración de la clave, de modo que un jugador conectado no transiciona por este motivo. Al restablecerse Redis, el heartbeat repuebla las claves |
| Autenticación de nuevas sesiones | No se puede consumir `ticket:jti:{jti}`, luego no se puede garantizar el anti-replay | Se rechaza el handshake: `UNAUTHORIZED`, cierre `4401`. **No** se acepta el ticket sin verificar el `jti`: un fallo de infraestructura nunca puede rebajar una garantía de seguridad |
| Idempotencia | Se pierde el camino rápido | Los comandos con efectos durables siguen protegidos por `idempotency_keys` en PostgreSQL. Los comandos sin efectos durables pueden re-ejecutarse; es una degradación aceptada y documentada |
| Locks y cooldowns | No hay coordinación entre procesos | Ver nota siguiente |
| Caché | Fallos de caché al 100 % | Se sirve desde PostgreSQL con mayor latencia. Sin pérdida de corrección |
| Simulación en curso | Ninguno | El game loop, los movimientos activos y la persistencia a PostgreSQL siguen funcionando: el mundo no se detiene |

Nota sobre locks: con una única instancia del game server la exclusión mutua se resuelve dentro del proceso y la caída de Redis no la compromete. Con varias instancias, la pérdida de Redis elimina la coordinación global, y la respuesta correcta es que las instancias no listas salgan de rotación (que es lo que ya hace `GET /ready`) en lugar de seguir operando sin garantías. El detalle del comportamiento multi-instancia es **TBD (fuera de MVP)**, porque el MVP despliega un único proceso de simulación.

Requisito de pruebas: existe un test de nivel *integration* que arranca el sistema, detiene Redis, verifica que ninguna escritura durable se pierde y que el proceso reporta correctamente su estado de readiness.

## Estado

**Aceptado** el 2026-09-09.

Se reevaluaría si `eo_redis_latency_seconds` mostrase que el round-trip a Redis consume una fracción significativa del procesamiento de comandos, o si la operación de un almacén adicional resultase desproporcionada frente a su beneficio en un despliegue de instancia única. En ese caso la alternativa a estudiar sería una caché en proceso **por delante** de Redis, no la eliminación de Redis: la coordinación multi-proceso y el anti-replay de tickets no tienen sustituto en memoria local.

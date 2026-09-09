# Recuperación ante desastres

Catálogo de escenarios de fallo con detección, impacto en jugadores, procedimiento paso a paso, criterios de éxito y prevención; recuperación de movimientos activos tras un corte; escalado y comunicación.

> Este documento asume que los backups existen, están verificados y son restaurables. Si esa premisa no se
> cumple, la mitad de los procedimientos siguientes no tiene desenlace. Ver
> [backups.md](./backups.md#8-un-backup-no-verificado-no-cuenta-como-backup).

---

## 1. Marco general

Empires Online se recupera bien de casi todo porque su diseño separa con rigor tres capas de estado, y cada
una tiene una estrategia de recuperación distinta:

| Capa | Contenido | Ante una pérdida |
|---|---|---|
| **PostgreSQL** — *durable source of truth* | Todo lo irrecuperable | Restaurar desde backup + PITR |
| **Redis** — *hot/transient* | Presencia, sesiones, locks, cooldowns, idempotencia, caché | **Se descarta y se reconstruye**; degradación temporal |
| **RAM del Game Server** — simulación activa | Mundo cargado, entidades, movimientos en curso | Se reconstruye desde Postgres al arrancar |

De ahí se deriva la jerarquía de gravedad, que conviene tener interiorizada antes de un incidente:

```
Redis vacío        →  degradación de minutos, cero pérdida durable
Game Server caído  →  indisponibilidad de ~15-60 s, cero pérdida durable
VPS perdido        →  indisponibilidad de 30-90 min, cero pérdida durable
Postgres perdido   →  indisponibilidad de hasta 60 min, pérdida ≤ 60 s (RPO)
Corrupción lógica  →  el escenario realmente difícil: los datos "válidos" están mal
```

El único caso donde no hay una salida mecánica es el último, porque un backup restaura estado, no
corrección.

### Clasificación de severidad

| Nivel | Definición | Ejemplos |
|---|---|---|
| **SEV-1** | Servicio caído o pérdida de datos en curso | Postgres corrupto, VPS perdido, despliegue que corrompe estado |
| **SEV-2** | Degradación grave con servicio parcialmente disponible | Redis caído, cola de persistencia desbordada, overruns críticos |
| **SEV-3** | Degradación acotada, sin riesgo de datos | Crash aislado con reinicio automático correcto, latencia elevada |

---

## E1. Crash del Game Server

**Severidad:** SEV-3 si el reinicio automático funciona; SEV-1 si entra en bucle de reinicio.

### Detección

- Alerta `EOReadinessDown` (`/ready` sin 200 durante 1 minuto).
- Alerta `EOLoopStalled` (`rate(eo_game_tick_duration_seconds_count[2m]) < 1`).
- `eo_connected_players` cae a 0 de golpe.
- `docker ps` muestra el contenedor reiniciando; los logs terminan en un pánico o un `SIGKILL`.

### Impacto en jugadores

Todas las conexiones se cierran. El cliente entra en reconexión con *backoff*. La indisponibilidad
percibida es de **~15–60 s**, la misma de un despliegue normal
(ver [deployment.md](./deployment.md#85-ventana-de-indisponibilidad-esperada)).

**Pérdida de estado:** el estado *write-through* (creación de entidades, inicio y fin de movimiento,
ownership, transiciones de presencia, `treaties`, `garrisons`, `world_events`) se aplica en RAM y su
transacción se encola de inmediato, con hasta 3 reintentos y compensación si se agotan. Se pierde, como
máximo:

- el estado con *dirty flag* desde el último flush —`EO_PERSISTENCE_FLUSH_INTERVAL_TICKS = 50` a 10 Hz son
  **5 segundos** de posiciones consolidadas y HP—;
- **más lo que estuviera encolado y sin COMMIT en el instante del crash.** Si el proceso muere entre aceptar
  un comando y confirmar su transacción (típicamente pocas decenas de ms), ese movimiento se pierde y la
  unidad queda en su última posición consolidada. Es un RPO documentado, no un descuido; `eo_persistence_queue_depth`
  en el momento de la caída es literalmente el tamaño de esa pérdida.

Las posiciones de unidades en movimiento ya persistido no se pierden en absoluto: son reconstruibles (§7).

### Procedimiento

1. **Comprueba si ya se recuperó solo.** `restart: unless-stopped` o la unidad systemd deberían haberlo
   reiniciado.
   ```bash
   docker ps --filter name=eo-game-server
   curl -fsS http://127.0.0.1:8080/ready
   ```
2. **Captura la causa antes de que la rotación de logs se la lleve.**
   ```bash
   docker logs eo-game-server --since 15m > /tmp/eo-crash-$(date -u +%Y%m%dT%H%M%SZ).log
   ```
   Busca `panic:`, la traza de goroutines, o un OOM (`docker inspect eo-game-server | grep -i oom`).
3. **Si está en bucle de reinicio, párala.** Un bucle no es una recuperación: cada ciclo recarga el mundo y
   reprocesa movimientos vencidos.
   ```bash
   docker compose stop game-server
   ```
   Diagnostica antes de volver a arrancar. Sospechosos habituales: configuración inválida (el fail-fast lo
   dirá con claridad, ver [configuration.md](./configuration.md#4-validación-al-arrancar-fail-fast)),
   Postgres o Redis inalcanzables, o datos que hacen fallar la carga del mundo.
4. **Si el crash es reproducible con la versión actual, haz rollback** a la imagen anterior por SHA
   (ver [deployment.md](./deployment.md#72-game-server-sin-migraciones)).
5. Verifica el arranque: `/health`, `/ready`, latido del loop, y un movimiento completo de extremo a extremo.

### Criterios de éxito

- `/ready` en 200 y `eo_game_tick_duration_seconds_count` creciendo a ~10/s.
- `eo_connected_players` recuperándose hacia el nivel previo.
- Ningún movimiento `ACTIVE` huérfano: la consulta del invariante `INV-MOVE-001`
  (ver [backups.md](./backups.md#63-verificación-antes-de-dar-el-servicio-por-restaurado), consulta 5)
  devuelve cero filas.
- Sin `INTERNAL_ERROR` sostenido.

### Prevención

- **Aislamiento de pánicos por comando.** `applyCommand` recupera el pánico de un comando concreto: un bug
  en una regla no tumba la simulación del mundo entero. El pánico se registra y el resto del tick continúa.
- Límite de memoria en el contenedor y vigilancia de `eo_persistence_queue_depth`: el OOM más probable viene
  de una cola que crece sin drenar.
- Tests de recuperación en `internal/game/simulation` (3 casos): movimiento vencido durante la caída,
  movimiento en curso reanudado y polilínea inválida. Están en verde.
- **Catch-up del loop:** el calendario es de tiempo absoluto y los ticks perdidos se descartan; jamás se
  ejecutan en ráfaga para "ponerse al día". Un reinicio no provoca una avalancha de ticks comprimidos.

---

## E2. VPS perdido

**Severidad:** SEV-1.

### Detección

- El VPS no responde a SSH ni a ping.
- Todas las sondas fallan a la vez: `/health`, `/ready`, TLS, WebSocket.
- El panel del proveedor confirma un incidente de host o de red.

### Impacto en jugadores

Servicio completamente caído. **Cero pérdida de datos durables**: Postgres y Redis son servicios
gestionados externos, no viven en el VPS. Se pierde el estado en RAM del proceso, que es reconstruible, y
como mucho los 5 segundos de estado con *dirty flag* no volcado.

### Procedimiento

1. Confirma el alcance: ¿el VPS o la región entera? Consulta el estado del proveedor.
2. **Verifica que Postgres y Redis siguen sanos.** Si lo están —y deberían, son servicios distintos—, el
   problema es solo de cómputo y la recuperación es un reaprovisionamiento, no una restauración.
3. **Aprovisiona un VPS nuevo** desde la configuración versionada (Caddyfile o Nginx, unidad systemd,
   `docker-compose.yml`, reglas de firewall). Este paso mide directamente si la infraestructura estaba
   descrita como código o vivía en la memoria de alguien.
4. Restaura el archivo de entorno `/etc/empires-online/game-server.env` desde el gestor de secretos, con
   permisos `0600` y propietario root.
5. **Añade la IP del nuevo VPS a las listas blancas** de Postgres y Redis. Este es el paso que se olvida y
   el que produce el desconcertante "todo está desplegado y `/ready` sigue en 503".
6. Despliega la imagen por SHA (la etiqueta exacta que corría antes; está en `/opt/empires-online/.image-tag`,
   y si el VPS se perdió, en el registro de despliegues).
7. Actualiza el DNS de `game.<dominio>` a la nueva IP. **El TTL del registro determina cuánto tardan los
   jugadores en llegar**: mantenerlo bajo (60–300 s) de forma permanente es lo que hace que este paso sea
   rápido en lugar de una espera de horas.
8. Verifica TLS (Caddy pedirá certificado nuevo: requiere puertos 80 y 443 abiertos), `/health`, `/ready` y
   una conexión real desde fuera.
9. Ejecuta el checklist de seguridad
   ([deployment.md](./deployment.md#9-checklist-de-seguridad-previo-a-producción)). Un VPS recién
   aprovisionado bajo presión es donde aparecen los puertos abiertos por accidente.

### Criterios de éxito

- `/ready` en 200; jugadores conectando por `wss://game.<dominio>/ws`.
- Certificado TLS válido.
- Puertos 8080 y 9090 **no** alcanzables desde Internet, verificado desde fuera.
- Movimiento completo de extremo a extremo.

### Prevención

- Infraestructura como código, versionada, y **probada aprovisionando de cero al menos una vez**.
- TTL de DNS bajo de forma permanente.
- Secretos en un gestor externo, nunca solo en el VPS.
- Postgres y Redis fuera del VPS: esta decisión es la que convierte "VPS perdido" en un incidente de una
  hora en lugar de una catástrofe.

**Tiempo estimado de recuperación:** 30–90 minutos, dominado por el aprovisionamiento y la propagación de DNS.

---

## E3. Postgres caído (sin corrupción)

**Severidad:** SEV-1.

### Detección

- `/ready` devuelve 503 con `"postgres":"error: ..."` en su mapa `checks`.
- `eo_database_latency_seconds` se dispara o desaparece.
- `eo_persistence_queue_depth` crece de forma monótona (alerta `EOPersistenceQueueCritical`).
- Logs con `"level":"ERROR"` sobre fallos de escritura tras agotar los 3 reintentos.

### Impacto en jugadores

Depende de cuánto dure. El Game Server sigue simulando: el mundo en RAM avanza, los movimientos progresan y
los deltas se emiten. Lo que **no** puede hacer es persistir. En la práctica:

- Los jugadores conectados siguen jugando, aparentemente con normalidad: el tick nunca hace I/O de Postgres,
  así que la simulación no se entera.
- La cola de persistencia crece sin drenar, y con ella la cantidad de estado que se perderá si el proceso
  muere. Los trabajos agotan sus 3 reintentos con backoff y disparan la compensación `OnPermanentFailure`.
- Cuando la cola se llena, el comando se descarta y el jugador recibe `INTERNAL_ERROR`. Ese es el momento en
  que la caída deja de ser invisible.

Ese "aparentemente con normalidad" es la trampa del escenario: **cuanto más se prolonga, más caro es el
final**.

### Procedimiento

1. Confirma que es Postgres y no la red: `/ready` desglosa las tres comprobaciones.
2. Consulta el panel del proveedor: ¿mantenimiento, failover en curso, agotamiento de conexiones, disco
   lleno?
3. **Si el proveedor tiene réplica con failover automático**, espera a que promueva y comprueba si el DSN
   sigue siendo válido. Muchos gestionados mantienen el mismo endpoint tras el failover.
4. **Decide con la cola en la mano.** Vigila `eo_persistence_queue_depth`:
   - si Postgres vuelve en pocos minutos y la cola es manejable, el sistema drena solo y no hay que hacer
     nada más;
   - si la cola se acerca al umbral crítico (10000) o la memoria del contenedor se estrecha, **detén el
     Game Server de forma ordenada**. Es contraintuitivo apagar un servicio que "funciona", pero un apagado
     ordenado drena la cola y evita la pérdida; un OOM la garantiza.
5. Cuando Postgres vuelva, arranca el Game Server y verifica el drenaje de la cola hasta cero.
6. Verifica el invariante `INV-MOVE-001` y el volumen de entidades.

### Criterios de éxito

- `/ready` en 200 con `"postgres":"ok"` en el mapa `checks`.
- `eo_persistence_queue_depth` de vuelta a su rango normal y estable.
- `eo_protocol_errors_total{code="INTERNAL_ERROR"}` plano; los comandos se aceptan.

### Prevención

- Postgres gestionado con alta disponibilidad y failover automático.
- Alertas de latencia de base de datos **antes** de que se convierta en caída
  (ver [monitoring.md](./monitoring.md#54-latencia-de-base-de-datos)).
- Pool de conexiones dimensionado y con timeouts, para que un Postgres lento no se traduzca en goroutines
  bloqueadas indefinidamente.

---

## E4. Postgres corrupto

**Severidad:** SEV-1. Es el escenario más grave con procedimiento mecánico.

### Detección

- Errores de integridad, fallos de suma de verificación, índices inconsistentes.
- Consultas de verificación que devuelven imposibles: unidades sin jugador, más de un movimiento `ACTIVE`
  por unidad, `world_chunks` incompleto.
- `world_state` ausente o con `epoch_ms` incoherente.

### Impacto en jugadores

Servicio detenido de inmediato y de forma deliberada. **Pérdida esperada: hasta el RPO de 60 segundos.**

### Procedimiento

1. **Detén el Game Server ya.** Cada segundo escribiendo contra una base corrupta amplía el daño y
   complica la elección del punto de restauración.
   ```bash
   cd /opt/empires-online && docker compose down
   ```
2. **Preserva la base corrupta.** Snapshot o backup del estado dañado antes de restaurar nada: es la única
   evidencia para investigar la causa.
3. **Determina el punto objetivo (PITR target).** La pregunta correcta es "¿cuándo se escribió la primera
   fila mala?", no "¿cuándo lo notamos?". Los logs, con su campo `tick` y sus `request_id`, permiten acotar.
4. **Restaura** siguiendo [backups.md](./backups.md#6-procedimiento-de-restauración), sobre una instancia
   nueva.
5. **Verifica** con las siete consultas de
   [backups.md §6.3](./backups.md#63-verificación-antes-de-dar-el-servicio-por-restaurado). Ninguna es
   opcional.
6. **Vacía Redis** (`FLUSHALL`). Tras un PITR, las claves de idempotencia pueden referirse a comandos que ya
   no existen en la base restaurada, y las de presencia a un estado que ya no aplica. Es seguro (§E5).
7. Apunta `EO_POSTGRES_URL` a la instancia restaurada y arranca el Game Server.
8. **Comunica qué se perdió**, con precisión y sin adornos.

### Criterios de éxito

- Las siete verificaciones en verde.
- `/ready` en 200 y el loop avanzando.
- Los jugadores reconectan y el mundo es coherente con lo que recuerdan, salvo el último minuto.
- La causa raíz de la corrupción está identificada, o hay una investigación abierta con evidencia guardada.

### Prevención

- Backups verificados y ensayos de restauración con periodicidad (ver [backups.md](./backups.md#82-cadencia-de-las-pruebas)).
- Restricciones en el esquema como red de seguridad activa: `CHECK` para los enums de dominio, claves
  foráneas, `version` para concurrencia optimista.
- Nunca escribir en la base de producción a mano. La mayoría de las corrupciones lógicas no las causa el
  motor: las causa una consulta escrita bajo presión.

---

## E5. Redis caído o vaciado

**Severidad:** SEV-2 si está caído; SEV-3 si solo se vació.

### Por qué un Redis vacío NO es una pérdida de datos irrecuperable

Merece explicación detallada, porque la intuición engaña: "he perdido una base de datos entera" suena a
catástrofe, y aquí no lo es.

El canon establece que **Redis nunca sustituye a PostgreSQL**. Redis es estado *hot/transient*, y todo lo
que contiene pertenece a una de dos categorías: **derivable** o **prescindible**. Ninguna clave de Redis es
la única copia de un hecho del mundo.

| Contenido | Clave | TTL | Qué pasa al desaparecer |
|---|---|---|---|
| Presencia del jugador | `presence:player:{playerId}` | 30 s | El heartbeat cada 10 s la reescribe. Se restablece sola en ≤ 10 s. **No gobierna la transición a `OFFLINE_PENDING`**: eso lo decide el loop en RAM |
| Idempotencia de comandos | `idem:{playerId}:{requestId}` | 300 s | Se pierde la deduplicación de los últimos 5 min. Los comandos **durables** siguen protegidos por `idempotency_keys` en Postgres. Además, si Redis no responde el comando **se ejecuta igualmente**: se prefiere jugar a bloquear al jugador |
| Anti-replay del ticket | `ticket:jti:{jti}` | 120 s | Un ticket interceptado podría reutilizarse dentro de su TTL de 60 s. Ventana estrecha y transitoria |
| Sesiones, locks, cooldowns, caché | varias | varios | Se recalculan desde Postgres o se repueblan por uso |

Qué **no** hay en Redis, y por eso no se pierde nada del mundo: posiciones de unidades, ownership, estado de
ciudades, movimientos, territorio, tratados, el mapa. Todo eso es Postgres.

### Degradación temporal concreta

Durante los segundos u horas que Redis esté vacío o ausente:

1. **Presencia.** Aquí la buena noticia es mayor de lo que parece: **la transición a `OFFLINE_PENDING` no
   depende de Redis en absoluto**. La decide el game loop en RAM, comparando el tiempo desde la última
   desconexión contra su `DisconnectGrace` (= `EO_PRESENCE_TTL_SECONDS`, 30 s), **no** leyendo la expiración
   de la clave. Redis mantiene la presencia para observadores externos y para el futuro multiproceso. Así
   que con Redis vacío **o caído**, nadie pasa a `OFFLINE_PENDING` por ese motivo: un jugador con una sesión
   WebSocket abierta sigue `ONLINE`, y uno con varias sesiones sigue `ONLINE` mientras le quede una. Lo que
   sí se pierde es la visibilidad externa de la presencia hasta que el heartbeat (cada 10 s) reescriba las
   claves.
2. **Idempotencia.** Un `requestId` repetido en la ventana de 5 minutos podría re-ejecutarse. La reserva se
   hace con `SETNX` en Redis **antes** de ejecutar, pero si Redis no responde el comando se ejecuta de todos
   modos: es una decisión consciente —se prefiere jugar a bloquear al jugador— y por eso Redis caído degrada
   la deduplicación en vez de detener el juego. Para comandos durables la protección real está en
   `idempotency_keys` (Postgres), así que el daño se limita a comandos no durables. Además, el invariante de
   movimiento contiene el efecto: **una unidad tiene como máximo un movimiento `ACTIVE`**, garantizado por un
   índice único parcial en la base, y una orden nueva cancela la anterior en la misma transacción. Un
   `unit.move` duplicado produce, en el peor caso, un movimiento cancelado (`reason: REPLACED`) y otro
   creado, no dos movimientos simultáneos.
3. **Anti-replay del ticket.** Sin `ticket:jti:{jti}`, un ticket capturado podría usarse dos veces dentro de
   su TTL de 60 s. Bajo TLS, capturarlo ya requiere un compromiso previo; y la ventana es de un minuto.
4. **Caché.** Más consultas a Postgres, latencia algo mayor. Vigila `eo_database_latency_seconds`.

**Lo que NO ocurre:** ninguna unidad pierde su posición, ningún movimiento se cancela, ninguna ciudad cambia
de dueño, ningún jugador pierde progreso. El mundo sigue exactamente donde estaba.

### Procedimiento

1. Confirma el alcance leyendo el mapa `checks` de `/ready`: `"redis":"error: ..."` (caído) frente a
   `"redis":"ok"` con claves ausentes (vaciado).
2. **Si está vaciado y accesible: no hay nada que hacer.** El sistema se repuebla solo. Vigila 15 minutos
   `eo_redis_latency_seconds`, `eo_connected_players` y las transiciones de presencia en los logs.
3. **Si está caído:** revisa el estado del proveedor. El Game Server debe degradar, no morir: `/ready`
   devuelve 503 (correcto: el servidor no está plenamente operativo), pero el loop sigue simulando y los
   jugadores conectados siguen jugando.
4. **No restaures Redis desde ningún backup.** No se respalda, y no debe respaldarse
   (ver [backups.md](./backups.md#2-por-qué-redis-no-se-respalda)). Levantar una instancia vacía es la
   recuperación correcta y completa.
5. Cuando vuelva, verifica que `/ready` regresa a 200 y que las claves de presencia reaparecen:
   ```bash
   redis-cli --scan --pattern "presence:player:*" | head
   ```
   En local, el equivalente es
   `docker compose exec redis redis-cli --scan --pattern "presence:player:*"`.
6. Revisa si alguna ciudad quedó en `OFFLINE_PENDING` o `PROTECTED` de forma indebida. **Debería ser raro**,
   porque esa transición no depende de Redis; si las hay, la causa está en otro sitio y merece investigarse.
   Si el jugador está conectado, la reconexión ya lo corrige.

### Criterios de éxito

- `/ready` en 200 con `"redis":"ok"` en el mapa `checks`.
- Las claves de presencia se están escribiendo (cadencia de 10 s).
- Sin estados de presencia anómalos para jugadores conectados.
- **Ninguna pérdida de estado durable**, comprobada con las consultas de volumen de entidades.

### Prevención

- Redis gestionado con alta disponibilidad.
- **Ensayar `FLUSHALL` en staging con jugadores simulados** y confirmar que degrada como aquí se describe.
  En local el Redis del compose arranca con AOF (`--appendonly yes`), así que la propiedad **no** se
  ejercita sola: hay que provocar el vaciado a mano, y merece la pena hacerlo al menos una vez por
  desarrollador (ver [local-development.md](./local-development.md#62-redis)).
- Revisiones de código con un criterio explícito: **cualquier escritura a Redis que no sea derivable ni
  prescindible es un bug de arquitectura**, y debe ir a Postgres.

---

## E6. Partición de red

**Severidad:** SEV-1 o SEV-2 según qué enlace se rompa.

### Detección

Depende del enlace afectado:

| Enlace roto | Síntoma |
|---|---|
| Jugadores ↔ VPS | `eo_connected_players` cae en picado, `/ready` sigue en 200 desde dentro del VPS |
| VPS ↔ Postgres | `checks.postgres` en error, la cola de persistencia crece (ver E3) |
| VPS ↔ Redis | `checks.redis` en error (ver E5) |
| Frontend ↔ jugadores | Nadie llega ni a intentar el handshake: `rate(eo_ws_messages_total{direction="inbound",type="session.hello"}[5m])` a cero |

La comprobación decisiva: **prueba desde fuera, no desde el VPS**. Muchas particiones solo se ven desde
Internet, y desde dentro todo parece perfecto.

### Impacto en jugadores

Desconexión total o parcial. **Sin pérdida durable** mientras Postgres siga alcanzable y el Game Server siga
persistiendo.

### Procedimiento

1. Localiza el enlace roto con la tabla anterior. No supongas.
2. **Jugadores ↔ VPS:** revisa DNS, certificado TLS, firewall, y el estado de red del proveedor.
   ```bash
   curl -fsSI https://game.<dominio>/
   dig +short game.<dominio>
   ```
3. **VPS ↔ Postgres/Redis:** verifica la lista blanca de IP (¿cambió la IP del VPS?), el peering privado y
   las reglas de seguridad. Una IP de VPS que cambia tras un reinicio del host es una causa clásica y
   desconcertante.
4. **Si la partición es del proveedor**, la acción es escalar y comunicar, no improvisar una migración a
   medias. Una migración precipitada durante una partición puede acabar con dos Game Servers vivos
   escribiendo en la misma base, que es un escenario mucho peor que estar caído.
5. Vigila `eo_persistence_queue_depth` mientras dure: si crece hacia el umbral crítico, aplica la decisión
   de E3 (apagado ordenado para drenar).

### Criterios de éxito

- Conectividad restablecida y verificada **desde fuera**.
- `/ready` en 200; jugadores reconectando.
- Cola de persistencia drenada.

### Prevención

- Sondas externas, no solo internas. Una monitorización que solo mira desde dentro del VPS no ve las
  particiones que afectan a los jugadores.
- IP estática en el VPS y listas blancas revisadas tras cualquier cambio de infraestructura.
- **Un único Game Server**, por diseño del MVP: elimina de raíz la posibilidad de *split-brain*.

---

## E7. Despliegue defectuoso

**Severidad:** SEV-1 si corrompe estado; SEV-2 si solo degrada.

### Detección

- Correlación temporal con un despliegue. Es la primera hipótesis, siempre.
- `eo_protocol_errors_total{code="INTERNAL_ERROR"}` subiendo tras el despliegue (alerta `EOInternalErrorsRising`).
- Overruns de tick que no existían antes.
- `eo_connected_players` que no se recupera tras la ventana normal de reinicio.
- Rechazos con códigos nuevos: `INVALID_MESSAGE` o `UNSUPPORTED_VERSION` apuntan a divergencia de esquema
  entre `packages/protocol` y el espejo embebido en el servidor. Los esquemas cliente→servidor son
  **estrictos** (`additionalProperties: false`), así que basta un campo de más.
- **El servidor no arranca y el log dice que el esquema está `dirty`**: una migración falló a medias. No lo
  fuerces; ver el punto 3 del procedimiento.

### Impacto en jugadores

Variable: desde errores esporádicos hasta imposibilidad de jugar. **El caso peligroso es el que escribe
estado incorrecto en Postgres**, porque el rollback de la imagen no deshace lo ya escrito.

### Procedimiento

1. **Decide rápido: ¿está corrompiendo datos?**
   - **Sí** → detén el Game Server **inmediatamente**. Parar es preferible a seguir escribiendo mal.
   - **No** → hay margen para diagnosticar unos minutos.
2. **Rollback de la imagen** al SHA anterior
   (ver [deployment.md](./deployment.md#72-game-server-sin-migraciones)).
3. **Si el despliegue incluía migraciones**, ten presente que el arranque solo migra **hacia adelante**:
   volver a la imagen anterior **no** deshace el esquema. Consulta la matriz de
   [deployment.md §7.3](./deployment.md#73-game-server-con-migraciones):
   - migración **aditiva** → basta volver la imagen; el código viejo ignora lo que no conoce;
   - migración **destructiva** → hay que ejecutar el `.down.sql` **a mano**, con el servidor detenido y
     revisión de otra persona, y aun así restaura la estructura pero **no los datos**; recupera los datos
     desde el backup previo a la migración
     (ver [backups.md](./backups.md#7-backup-lógico-previo-a-migración-destructiva));
   - esquema **`dirty`** → el servidor no arrancará hasta que alguien resuelva la migración a medias y
     corrija `schema_migrations`. Es intervención manual por diseño: continuar automáticamente sería
     adivinar.
4. **Si se escribió estado incorrecto**, evalúa el alcance antes de elegir:
   - daño acotado a filas identificables → corrección quirúrgica con una consulta revisada por otra persona,
     y backup previo;
   - daño extenso → PITR al instante anterior al despliegue (E4), asumiendo la pérdida del juego posterior.
5. Verifica y observa 30 minutos.
6. **Post-mortem obligatorio.** La pregunta útil no es "¿qué falló?" sino "¿por qué CI no lo detectó?".

### Criterios de éxito

- La versión anterior corre y `/ready` está en 200.
- Métricas de vuelta a la línea base previa al despliegue.
- El alcance del daño en datos está evaluado y documentado, aunque sea nulo.

### Prevención

- CI completo obligatorio: `pnpm run verify` (`protocol:build → docs:check → typecheck → test →
  server:fmt:check → server:vet → server:test`), más `protocol:check` y los tests de integración con
  `EO_INTEGRATION=1`, que `verify` no ejecuta. Un PR con cualquier check obligatorio en rojo no es válido.
  **Los tests de integración solo cuentan si de verdad se ejecutaron**: sin `EO_INTEGRATION=1` se saltan y el
  verde no significa nada (ver [local-development.md](./local-development.md#9-ejecutar-los-tests)).
- Staging con el mismo procedimiento de despliegue que producción; si staging es distinto, no prueba nada.
- **Migraciones en dos fases**: aditiva primero, destructiva después, de modo que el rollback sea siempre un
  cambio de imagen.
- Etiquetas de imagen por SHA de commit, nunca `latest`: sin ellas no hay rollback.
- Verificación post-despliegue que incluye un movimiento de extremo a extremo, no solo un `/ready` en 200.

---

## E8. Corrupción lógica del estado del mundo por un bug

**Severidad:** SEV-1. El escenario más difícil, porque los datos son **válidos** para la base de datos y
**erróneos** para el juego.

### Detección

Rara vez la detecta la monitorización. La detectan **los jugadores**, y por eso los reportes merecen ser
tratados como señal técnica:

- "Mi unidad apareció dentro de una montaña."
- "Perdí mi ciudad y no me atacó nadie."
- "Mi aldeano lleva media hora sin llegar."

Señales técnicas correlacionadas:

```sql
-- Más de un movimiento ACTIVE por unidad (viola INV-MOVE-001).
-- Debería ser imposible: lo impide el índice único parcial unit_movements_one_active_per_unit.
-- Si devuelve filas, sospecha del propio índice antes que de la lógica.
SELECT unit_id, count(*) FROM unit_movements WHERE status='ACTIVE' GROUP BY unit_id HAVING count(*) > 1;

-- Movimientos ACTIVE cuya llegada venció hace mucho: el loop no los está finalizando
SELECT count(*) FROM unit_movements
WHERE status='ACTIVE' AND arrival_time_ms < (EXTRACT(EPOCH FROM now())*1000)::bigint - 60000;

-- Unidades fuera de los límites del mundo (512x512 con la configuración por defecto)
SELECT id, x, y FROM units WHERE x < 0 OR y < 0 OR x >= 512 OR y >= 512;

-- Unidades cuyo chunk desnormalizado no cuadra con su posición.
-- chunk_x/chunk_y son la clave del interest management: si divergen, el jugador
-- deja de ver entidades que deberían estar en su área.
-- El 32 es EO_CHUNK_SIZE: ajústalo si el despliegue usa otro valor.
SELECT id, x, y, chunk_x, chunk_y FROM units
WHERE status <> 'DEAD' AND (chunk_x <> x / 32 OR chunk_y <> y / 32);

-- Ciudades sin jugador. Ojo al nombre de la columna: en `cities` el dueño es
-- `owner_player_id` (en `units` es `player_id`).
SELECT c.id FROM cities c LEFT JOIN players p ON p.id = c.owner_player_id WHERE p.id IS NULL;

-- Ciudades que violan su propio CHECK de población (red de seguridad del esquema)
SELECT id, population, population_limit FROM cities WHERE population > population_limit;
```

### Impacto en jugadores

Injusticia percibida, que en un mundo persistente con riesgo es el daño más difícil de reparar: no se pierde
solo estado, se pierde confianza. Y el efecto se agrava con el tiempo, porque el estado erróneo sigue
propagándose por la simulación.

### Procedimiento

1. **Recoge evidencia antes de tocar nada.** Consultas que documenten el estado anómalo, con sus filas y sus
   identificadores.
2. **Acota el alcance.** ¿Una unidad, un jugador, o todo el mundo? La respuesta cambia por completo la
   estrategia.
3. **Determina cuándo empezó.** Correlaciona con despliegues y con los logs por `tick`. `world_events` es
   aquí la fuente más valiosa: es la historia de hechos consumados del mundo.
4. **Detén la propagación.** Si el bug sigue produciendo estado malo, detén el Game Server o despliega el
   arreglo. No tiene sentido reparar mientras la fuente del daño sigue activa.
5. **Elige la estrategia de reparación:**

   | Alcance | Estrategia |
   |---|---|
   | Pocas entidades identificables | Corrección quirúrgica: consulta revisada por otra persona, con backup previo y registro de lo cambiado |
   | Un sistema entero, pero acotado en el tiempo | Restauración selectiva de las tablas afectadas desde el volcado lógico (ver [backups.md](./backups.md#62-restauración)) |
   | Mundo entero e irreparable | PITR al instante anterior al primer efecto del bug (E4), asumiendo la pérdida del juego posterior |

   El criterio de decisión es el balance entre dos daños: el estado erróneo que se conserva frente al juego
   legítimo que se destruye al revertir. Un PITR de ocho horas para arreglar tres unidades mal colocadas es
   una cura peor que la enfermedad.
6. **Corrige el bug**, con un test de regresión que lo reproduzca, antes de volver a desplegar.
7. **Comunica con transparencia**: qué pasó, a quién afectó, qué se hizo y qué no se pudo reparar.

### Criterios de éxito

- Las consultas de detección devuelven cero filas.
- Los invariantes se verifican: `INV-WORLD-001`, `INV-PLAYER-001`, `INV-CITY-001`, `INV-UNIT-001`,
  `INV-MOVE-001`, `INV-PERSIST-001`, `INV-SEC-001`
  (ver [../invariants/README.md](../invariants/README.md)).
- El bug tiene test de regresión y está corregido.
- Los jugadores afectados han sido informados.

### Prevención

- **Invariantes con ID estable, testeados**, no solo documentados. Un invariante que no se comprueba en un
  test es un comentario.
- Comprobaciones periódicas de invariantes en producción, con alerta ante violación. Esta es la diferencia
  entre enterarse en una hora o en una semana.
- Determinismo estricto: nada de `time.Now()` ni `rand` dentro del dominio; `Clock` y `RandomSource`
  inyectados. Un bug determinista es reproducible; uno no determinista puede ser imposible de encontrar.
- Tests de simulación ("avanza 10 s, asserta estado exacto") y de recuperación en cada PR: hoy existen y
  están en verde en `internal/game/simulation`, incluidos el *vertical slice* completo, el reemplazo de
  orden, la cancelación, los 6 rechazos y la reproducibilidad.
- Restricciones en el esquema como última línea de defensa: todos los enums de dominio son `text` + `CHECK`
  (no tipos ENUM de PostgreSQL), más claves foráneas, el `CHECK population <= population_limit`, el
  `CHECK hp <= max_hp`, el `UNIQUE (center_x, center_y)` de `cities`, el índice único parcial de movimientos
  activos y la columna `version` para concurrencia optimista.

---

## 7. Recuperación de movimientos activos tras un corte

Esta sección merece detalle porque es la propiedad que hace que un reinicio sea inocuo, y porque contiene
un caso que despista.

### 7.1 Por qué no se pierde nada

Un movimiento no se guarda como "la unidad está aquí ahora". Se guarda como una **polilínea temporizada**:
un array de waypoints `{x, y, tMs}`, donde `tMs` es el offset en milisegundos desde `start_time_ms` en el
que la unidad **alcanza** ese tile. El primer waypoint es el origen, con `tMs = 0`.

De ahí se deriva la regla que lo resuelve todo:

> **Posición autoritativa en el instante T = último waypoint con `tMs <= (T - start_time_ms)`.**

Es **analíticamente reconstruible**: no requiere reproducir ticks, ni conocer cuánto tiempo estuvo caído el
servidor, ni haber guardado nada durante el trayecto. Por eso el canon clasifica la posición durante un
movimiento activo como *reconstruible*, no como estado a persistir cada tick.

```
start_time_ms                          arrival_time_ms
      │                                       │
      ▼                                       ▼
      ●────────●────────●────────●────────────●
   tMs=0     600      1449     2409         2769
                       ▲
                corte del servidor
                (T - start_time_ms = 2100 ms)
                último waypoint con tMs <= 2100  →  tMs=1449
                la unidad se restablece EXACTAMENTE ahí
```

El ejemplo es el canónico del dominio, con un `VILLAGER` (`baseMsPerTile = 600`):

```
(0,0) -> (1,0)  GRASSLAND ortogonal   600      acc    0 -> 600
(1,0) -> (2,1)  GRASSLAND diagonal    849      acc  600 -> 1449
(2,1) -> (3,1)  FOREST    ortogonal   960      acc 1449 -> 2409
(3,1) -> (4,1)  ROAD      ortogonal   360      acc 2409 -> 2769
```

Los `tMs` no son múltiplos regulares porque cada segmento cuesta
`(baseMsPerTile * costUnits + 5) / 10`, y si el paso es diagonal se multiplica además por √2 en punto fijo
(`(ms * 1414214 + 500000) / 1000000`). **La regla canónica es que se redondea cada segmento al milisegundo
más cercano y solo después se acumula** — no se trunca: mil pasos truncados regalarían casi un segundo de
ventaja. Con `VILLAGER`: `GRASSLAND` (costUnits 10) cuesta **600** ms en ortogonal y **849** en diagonal;
`FOREST` (16), **960** y **1358**; `HILL` (18), **1080** y **1527**; `ROAD` (6), **360** y **509**.

### 7.2 Procedimiento de arranque

Al arrancar, el servidor carga todos los movimientos con `status = 'ACTIVE'` y aplica exactamente esta
regla:

```
para cada movimiento con status = 'ACTIVE':
    si la polilínea es inválida (no parsea, vacía, incoherente):
        → el movimiento pasa a FAILED
        → la unidad SE QUEDA DONDE ESTABA
    si no, si arrival_time_ms <= now:
        → la unidad hace SNAP al tile final de la polilínea
        → el movimiento pasa a COMPLETED
        → se emite unit.movement.completed
    si no:
        → posición = último waypoint con tMs <= (now - start_time_ms)
        → el movimiento se REANUDA desde ese punto de la polilínea
        → sigue ACTIVE; llegará a arrival_time_ms como estaba previsto
```

La rama `FAILED` es tan importante como las otras dos: ante un dato dudoso el servidor **no teletransporta a
nadie**. Deja la unidad quieta, cierra el movimiento y lo cuenta. El recuento de las tres ramas aparece en el
log de arranque, en la línea `mundo rehidratado`, como `movements_resumed`,
`movements_arrived_while_down` y `movements_failed`. **Un `movements_failed` distinto de cero merece
investigación aunque el servidor haya arrancado bien**: significa que hay polilíneas corruptas en la base.

### 7.3 El caso de `arrival_time_ms` ya vencido

Es el caso que sorprende, y conviene entenderlo bien.

**Escenario.** Una unidad sale a las 18:00:00 con `arrival_time_ms` correspondiente a las 18:00:12. El
servidor cae a las 18:00:05 y no vuelve hasta las 18:47:00. El movimiento sigue `ACTIVE` en la base porque
nadie pudo finalizarlo.

**Qué hace el servidor al arrancar.** `arrival_time_ms` (18:00:12) es anterior a `now` (18:47:00), así que
el movimiento **se completa inmediatamente**: la unidad hace *snap* al tile final, el movimiento pasa a
`COMPLETED` y se emite `unit.movement.completed`.

**Por qué esto es correcto, y no un atajo:**

1. **El mundo persistente no se detiene.** El canon es explícito: el mundo evoluciona sin jugadores
   conectados, y ningún estado durable depende de un WebSocket vivo. El tiempo transcurrió; la unidad
   *debería* haber llegado; llegó.
2. **La alternativa sería peor.** Reanudar el movimiento "desde donde estaba" 47 minutos después implicaría
   que el tiempo del mundo se pausó mientras el servidor estaba caído. Eso rompería la relación entre
   `tickTime` y el reloj real, y convertiría cada caída en una distorsión temporal permanente del mundo.
3. **El destino era conocido y validado.** El path se calculó y se validó cuando se dio la orden. No se
   está inventando nada: se está aplicando el resultado ya determinado.
4. **Es determinista.** Dos servidores que arrancasen con la misma base y el mismo instante producirían
   exactamente el mismo resultado.

**Qué ve el jugador.** Al reconectar, su aldeano está en el destino que ordenó. Desde su punto de vista es
lo correcto y lo esperado — le dio una orden y se cumplió.

**Qué no se recalcula.** El *snap* usa el tile final de la polilínea persistida, no una nueva búsqueda A\*.
Si el terreno cambiase durante la caída (bloqueo dinámico por un edificio nuevo), el destino podría haber
dejado de ser transitable. En MVP el bloqueo dinámico no cambia sin jugadores actuando, así que el caso no
se materializa; la reconciliación de destinos invalidados durante una caída queda como
**TBD (fuera de MVP)**.

### 7.4 Invariante que hay que verificar después

Tras cualquier recuperación, comprueba `INV-MOVE-001` — **una unidad tiene como máximo un movimiento
`ACTIVE`**:

```sql
SELECT unit_id, count(*) AS active
FROM unit_movements
WHERE status = 'ACTIVE'
GROUP BY unit_id
HAVING count(*) > 1;   -- esperado: cero filas
```

Cualquier fila aquí indica una recuperación mal ejecutada o un bug en la creación de movimientos, donde una
orden nueva no canceló la anterior en la misma transacción.

---

## 8. Escalado y comunicación

### 8.1 Escalado

| Nivel | Cuándo | Acción |
|---|---|---|
| **N1 — Runbook** | Síntoma cubierto por [monitoring.md §6](./monitoring.md#6-runbook-qué-mirar-primero) | Seguir el runbook. Límite de tiempo: **15 minutos**. Si no está resuelto, subir |
| **N2 — Incidente** | SEV-2, o un N1 que superó su límite | Declarar incidente, abrir registro cronológico, notificar al equipo |
| **N3 — Desastre** | SEV-1: pérdida de datos, servicio caído, corrupción | Todo el equipo disponible. Decisiones de restauración con **dos personas**, nunca una sola |

Regla de oro durante un SEV-1: **nadie ejecuta una acción destructiva en solitario**. Una restauración, un
`FLUSHALL` o una consulta de corrección se revisan con otra persona antes de ejecutarse. La mayoría de los
incidentes que empeoran, empeoran por una acción apresurada durante el propio incidente.

### 8.2 Registro del incidente

Desde el minuto uno, y en UTC:

```
[HH:MM] Detectado: <síntoma>. Fuente: <alerta / reporte de jugador>
[HH:MM] Severidad asignada: SEV-<n>
[HH:MM] Hipótesis: <...>
[HH:MM] Acción: <qué se hizo> — Resultado: <qué pasó>
[HH:MM] Decisión: <qué se decidió y por qué>
[HH:MM] Servicio restablecido
[HH:MM] Pérdida confirmada: <alcance exacto>
```

El registro no es burocracia: es lo que permite escribir un post-mortem honesto tres días después, cuando
nadie recuerda el orden real de los hechos.

### 8.3 Comunicación a jugadores

Principios, en orden de importancia:

1. **Rápido antes que completo.** Un mensaje en 5 minutos diciendo "sabemos que hay un problema y lo estamos
   investigando" vale más que un análisis perfecto en dos horas.
2. **Honesto sobre la pérdida.** Si se perdió un minuto de juego, se dice: "se perdieron las órdenes
   emitidas entre las 18:41 y las 18:42 UTC". Minimizar destruye la confianza de forma permanente; en un
   mundo persistente con riesgo, la confianza en que el servidor es justo es el producto.
3. **Sin jerga.** "El servidor se reinició y las unidades volvieron a su posición correcta" comunica; "se
   ejecutó un PITR con recovery_target_time" no.
4. **Con cierre.** Todo incidente comunicado necesita un mensaje final, aunque sea breve.

Plantillas:

```
[EN CURSO] Estamos investigando un problema que impide conectar al mundo.
Vuestro progreso está a salvo. Próxima actualización en 30 minutos.
```

```
[RESUELTO] El servidor volvió a la normalidad a las HH:MM UTC.
Causa: <explicación en una frase>.
Impacto: <qué se perdió, o "no se perdió progreso">.
Sentimos las molestias.
```

Ante una pérdida de datos real, además: qué se perdió exactamente, en qué franja horaria, a cuántos
jugadores afectó y qué se hará para que no se repita.

### 8.4 Post-mortem

Obligatorio para todo SEV-1 y SEV-2, dentro de las 72 horas. Sin culpables: se buscan causas de sistema, no
personas.

Contenido mínimo: cronología, causa raíz, impacto medido (duración, jugadores afectados, datos perdidos),
qué funcionó de este documento, qué no, y acciones correctivas con responsable y fecha. Toda decisión de
diseño que salga del post-mortem se registra como ADR en [../decisions/](../decisions/).

---

## Referencias

- [backups.md](./backups.md) — restauración, PITR, RPO y RTO.
- [deployment.md](./deployment.md) — rollback, reinicio y checklist de seguridad.
- [monitoring.md](./monitoring.md) — detección, alertas y runbook de primer nivel.
- [configuration.md](./configuration.md) — validación fail-fast y rotación de secretos.
- [local-development.md](./local-development.md) — ensayo local del vaciado de Redis.
- [../architecture/game-loop.md](../architecture/game-loop.md) — fases del tick y recuperación al arrancar.
- [../invariants/README.md](../invariants/README.md) — invariantes a verificar tras recuperar.
- [../invariants/movement.md](../invariants/movement.md) — `INV-MOVE-001` y el resto de la familia.
- [../decisions/ADR-011-movement-timed-polyline.md](../decisions/ADR-011-movement-timed-polyline.md) — el movimiento como polilínea temporizada.
- [../decisions/ADR-004-redis-hot-state.md](../decisions/ADR-004-redis-hot-state.md) — por qué perder Redis no es perder el mundo.

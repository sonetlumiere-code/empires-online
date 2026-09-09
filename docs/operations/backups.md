# Copias de seguridad

Política de respaldo de Empires Online: qué se respalda y qué no, backups completos más PITR, objetivos de RPO y RTO, retención, cifrado, almacenamiento externo, procedimiento de restauración verificado y backup previo a migración destructiva.

> Regla que gobierna este documento: **un backup no verificado no es un backup, es una esperanza**. Un
> archivo que nunca se ha restaurado tiene una probabilidad desconocida de servir, y la probabilidad
> desconocida se descubre siempre en el peor momento. La sección §6 no es opcional.

---

## 1. Qué se respalda

| Componente | ¿Se respalda? | Motivo |
|---|---|---|
| **PostgreSQL** | **Sí, completo, con PITR** | Es la *durable source of truth*. Todo lo irrecuperable vive aquí |
| **Redis** | **No** | Estado *hot/transient*, reconstruible por diseño (§2) |
| RAM del Game Server | No | Simulación activa; reconstruible desde Postgres al arrancar |
| Imágenes Docker | No como backup | Reproducibles desde el repositorio y el registro, etiquetadas por SHA |
| Configuración del VPS (Caddyfile, unidad systemd, compose) | Sí, en el repositorio de infraestructura | Es código, no datos |
| Secretos (`EO_AUTH_JWT_SECRET`, DSN) | Sí, en el gestor de secretos, **nunca junto a los backups** | Un backup cifrado y su clave en el mismo sitio no está cifrado |
| Frontend en Vercel | No | Vercel conserva los despliegues; reconstruible desde el repositorio |

Dentro de PostgreSQL, el respaldo es **completo**: las 17 tablas que crea la migración `000001`
(`civilizations`, `factions`, `eras`, `players`, `world_state`, `world_chunks`, `cities`, `units`,
`unit_movements`, `sessions`, `idempotency_keys`, `territories`, `territory_control`, `safe_zones`,
`treaties`, `garrisons`, `world_events`) más la tabla `schema_migrations` que mantiene golang-migrate. No se
excluye ninguna tabla "para ahorrar espacio": una restauración parcial produce un mundo inconsistente, que es
peor que no tener mundo. Y `schema_migrations` menos que ninguna: sin ella, el binario no sabe en qué versión
está el esquema restaurado.

Merece mención explícita `world_chunks`, porque hoy **no es la fuente primaria del mapa**: el terreno se
regenera desde `EO_WORLD_SEED = 20260909` en cada arranque, y la tabla guarda una copia escrita la primera
vez, para auditoría y para permitir mapas editados en el futuro. Aun así se respalda, por dos razones: es la
única evidencia de qué mapa había en un momento dado, y en cuanto exista una sola edición manual del mapa
dejará de ser derivable de la semilla. Excluirlo sería apostar a que nadie tocará nunca un tile.

Lo que **sí** es irreemplazable y no deriva de nada es el resto: jugadores, ciudades, unidades, movimientos,
territorio, tratados, guarniciones y `world_events`.

---

## 2. Por qué Redis no se respalda

Redis contiene, y solo contiene, estado que el sistema sabe reconstruir o del que puede prescindir:

| Contenido | Clave | TTL | Qué pasa si desaparece |
|---|---|---|---|
| Presencia del jugador | `presence:player:{playerId}` | 30 s (`EO_PRESENCE_TTL_SECONDS`) | El heartbeat (cada 10 s) la reescribe. Y nadie pasa a `OFFLINE_PENDING` por perderla: esa transición la decide el game loop en RAM contra su propio `DisconnectGrace`, no leyendo la expiración de la clave. Redis mantiene la presencia para observadores externos y para el futuro multiproceso |
| Idempotencia de comandos | `idem:{playerId}:{requestId}` | 300 s | Se pierde la deduplicación en memoria de los últimos 5 minutos. Los comandos **durables** siguen protegidos por la tabla `idempotency_keys` en Postgres |
| Anti-replay del ticket | `ticket:jti:{jti}` | 120 s | Un ticket interceptado podría reutilizarse dentro de su TTL de 60 s. Riesgo acotado y temporal |
| Sesiones, locks, cooldowns, caché | varias | varios | Se recalculan o se repueblan por uso |

La razón de fondo es de arquitectura, no de comodidad: el canon establece que **Redis nunca sustituye a
PostgreSQL** (ver [ADR-004](../decisions/ADR-004-redis-hot-state.md)). Si algo se perdiera de forma
irrecuperable al vaciar Redis, sería porque se guardó allí estado que debía estar en Postgres — un bug de
diseño que un backup de Redis se limitaría a ocultar.

En local, el Redis del `docker-compose.yml` arranca con `--appendonly yes --save ''`: AOF sí, snapshots RDB
no. Ese AOF es **una comodidad de desarrollo, no una garantía de diseño**, y no cambia nada de lo anterior.
Por eso la propiedad hay que ejercitarla a mano, con un `FLUSHALL` deliberado
(ver [local-development.md](./local-development.md#62-redis)): convierte esa clase de error en un fallo
visible durante el desarrollo, en lugar de en una sorpresa en producción.

El detalle de la degradación temporal está en
[disaster-recovery.md](./disaster-recovery.md#e5-redis-caído-o-vaciado).

---

## 3. Estrategia: completo diario + PITR con WAL

Dos mecanismos complementarios. Ninguno sustituye al otro.

```
        base backup                 base backup
        (completo, diario)          (completo, diario)
             │                            │
   ══════════●════════════════════════════●═══════════════════▶ tiempo
             └── WAL ── WAL ── WAL ── WAL ─┴── WAL ── WAL ──▶
                        ▲
                        └── restauración a CUALQUIER instante
                            entre el base backup y el último WAL archivado
```

### 3.1 Backup completo diario (base backup)

- **Cadencia:** diario, en la franja de menor actividad.
- **Método:** backup físico completo del clúster (`pg_basebackup` o el mecanismo equivalente del proveedor
  gestionado), consistente y sin bloquear escrituras.
- **Verificación inmediata:** al terminar se comprueba integridad y se registra tamaño, duración y suma de
  verificación. Un backup que no se completó debe generar alerta, no un silencio.

### 3.2 Archivado continuo de WAL

- **Modo:** `wal_level = replica` como mínimo y archivado continuo activo.
- **`archive_timeout = 60s`:** fuerza el cierre y archivado de un segmento WAL al menos cada minuto, aunque
  no se haya llenado. Este parámetro **es** el que determina el RPO real: sin él, en periodos de baja
  actividad podrían pasar horas hasta que un segmento se archive, y todo ese intervalo sería pérdida
  potencial.
- **Destino:** almacenamiento de objetos externo, el mismo del §5.
- **Requisito temporal absoluto:** el archivado debe estar activo **antes del primer jugador**. No existe
  recuperación a un punto anterior al inicio del archivado; esa ventana simplemente no es recuperable.

### 3.3 Backup lógico semanal

Un `pg_dump` lógico semanal, además de los físicos. No sustituye al PITR: sirve para lo que el backup
físico no puede hacer.

| | Backup físico + WAL | Backup lógico (`pg_dump`) |
|---|---|---|
| Restaura a un instante exacto | Sí | No, solo al momento del volcado |
| Restaura una sola tabla | No | Sí |
| Portable entre versiones mayores de Postgres | No | Sí |
| Legible e inspeccionable | No | Sí |
| Velocidad de restauración completa | Alta | Baja |

El caso de uso real del lógico es acotado y valioso: "un bug borró filas de `treaties` el martes; necesito
esas filas, y solo esas, sin revertir el mundo entero".

```bash
# Volcado lógico comprimido, formato custom (permite restauración selectiva por tabla)
pg_dump --format=custom --compress=9 \
        --dbname="$EO_POSTGRES_URL" \
        --file="eo-logical-$(date -u +%Y%m%dT%H%M%SZ).dump"
```

En una máquina de desarrollo Windows sin `psql` ni `pg_dump` instalados, el equivalente pasa por el
contenedor —y contra la base local, que se llama `empires`—: ver
[local-development.md](./local-development.md#61-postgres).

---

## 4. RPO y RTO

| Objetivo | Valor | Qué significa |
|---|---|---|
| **RPO** (pérdida máxima aceptable de datos) | **≤ 60 segundos** | Ante una pérdida total de Postgres se recupera hasta el último segmento WAL archivado. Con `archive_timeout = 60s`, la ventana no supera el minuto |
| **RTO** (tiempo máximo hasta servicio restablecido) | **≤ 60 minutos** | Desde la declaración del incidente hasta que `/ready` devuelve 200 y los jugadores pueden conectar |

Desglose realista del RTO, para que el número no sea un deseo:

| Fase | Estimación |
|---|---|
| Detección y decisión de restaurar | 5–10 min |
| Aprovisionamiento de la instancia de destino | 5–10 min |
| Restauración del base backup | 10–25 min (depende del tamaño del clúster) |
| Aplicación de WAL hasta el punto objetivo | 2–10 min |
| Verificación de consistencia (§6.3) | 5 min |
| Repunte del Game Server y reconexión de jugadores | 1–2 min |

**Pérdida efectiva para el jugador.** Aquí conviene ser preciso, porque el RPO de un minuto suena peor de lo
que es. Lo que se pierde en ese minuto no es "un minuto de juego":

- El estado **write-through** (creación de jugador/ciudad/unidad, inicio y finalización de movimiento,
  cambios de ownership, transiciones de presencia, `treaties`, `garrisons`, `world_events`) se aplica en RAM
  y su transacción se **encola inmediatamente** en la cola de persistencia, con hasta 3 reintentos y
  backoff: solo se pierde lo ocurrido en el último minuto no archivado, más la ventana de decenas de
  milisegundos entre aceptar un comando y hacer COMMIT de su transacción.
- El estado con **dirty-flag** (posiciones consolidadas, HP) se vuelca cada
  `EO_PERSISTENCE_FLUSH_INTERVAL_TICKS = 50` ticks, es decir cada **5 segundos**, así que su desfase
  adicional es despreciable frente al RPO.
- Las **posiciones durante un movimiento activo** no se pierden en absoluto aunque no estuvieran escritas:
  son reconstruibles analíticamente desde la polilínea temporizada de `unit_movements`.

Es decir: un incidente en el peor punto del ciclo devuelve al jugador a un mundo con hasta un minuto menos
de órdenes registradas, no a un mundo destrozado.

---

## 5. Retención, cifrado y almacenamiento externo

### 5.1 Retención

| Tipo | Retención | Justificación |
|---|---|---|
| Base backup diario | **14 días** | Cubre dos semanas de incidentes; suficiente para detectar corrupción que se manifiesta tarde |
| WAL archivado | **14 días** | Debe cubrir al menos el base backup más antiguo: sin sus WAL, ese base backup solo permite restaurar a su propio instante, no a un punto posterior |
| Backup lógico semanal | **8 semanas** | Recuperación selectiva de tablas y auditoría a medio plazo |
| Backup lógico mensual (primero del mes) | **6 meses** | Retención larga con coste bajo |
| Backup previo a migración destructiva | **90 días** | Se conserva más que un backup rutinario porque su valor es específico y su momento, irrepetible |

Regla que se incumple con facilidad: **la retención del WAL nunca puede ser menor que la del base backup más
antiguo que se pretende usar**. Un base backup de 14 días con solo 3 días de WAL es un base backup mutilado.

### 5.2 Cifrado

- **En reposo:** todo backup se cifra con AES-256 antes de salir del entorno. El cifrado del almacenamiento
  del proveedor cuenta como una capa, no como *la* capa: si la cuenta se ve comprometida, ese cifrado es
  transparente para el atacante.
- **En tránsito:** TLS obligatorio hacia el destino de almacenamiento.
- **Claves:** viven en el gestor de secretos, **nunca junto a los backups**, nunca en el repositorio.
  Ver [configuration.md](./configuration.md#6-manejo-de-secretos).
- **Rotación de la clave de cifrado:** anual, o inmediata ante sospecha de compromiso. Los backups
  existentes conservan su clave original, así que la clave retirada debe conservarse mientras exista un
  backup cifrado con ella. Retirar una clave antes que sus backups los destruye.

### 5.3 Almacenamiento fuera del proveedor principal

**Al menos una copia diaria vive en un proveedor distinto del que aloja Postgres.**

La razón no es la falta de fiabilidad técnica del proveedor, sino la clase de fallo que no se puede
descartar: cierre o suspensión de la cuenta, error de facturación, compromiso de credenciales, borrado
accidental que se replica a todas las réplicas de esa misma cuenta, o incidente regional. Un backup que solo
existe dentro del mismo dominio de fallo que el sistema que protege comparte su destino.

Regla mínima:

- **Copia primaria:** almacenamiento del proveedor gestionado, misma región. Restauración rápida.
- **Copia secundaria:** almacenamiento de objetos de **otro** proveedor u otra cuenta con credenciales
  independientes, y en otra región. Cadencia diaria, retención de 14 días.
- Las credenciales de escritura de la copia secundaria son **de solo escritura y adición**: el proceso de
  backup no debe poder borrar backups previos. Si un atacante compromete el VPS, no debe poder destruir el
  histórico.
- Versionado y bloqueo de objetos activados en el bucket de destino cuando el proveedor lo permita.

---

## 6. Procedimiento de restauración

### 6.1 Antes de tocar nada

1. **Declara el incidente** y anota la hora. Ver
   [disaster-recovery.md](./disaster-recovery.md#8-escalado-y-comunicación).
2. **Determina el punto objetivo de recuperación (PITR target).** Es la decisión más importante de todo el
   procedimiento. Ante una corrupción lógica por un bug, el objetivo es el instante **inmediatamente
   anterior** al primer efecto del bug, no "hace una hora".
3. **Detén el Game Server.** Un servidor escribiendo contra una base que estás restaurando produce un
   estado híbrido peor que el original.
   ```bash
   cd /opt/empires-online && docker compose down
   ```
4. **Preserva la evidencia.** Si la base original es accesible, haz un backup de su estado corrupto
   **antes** de restaurar. Es la única oportunidad de investigar la causa después.
5. **Restaura sobre una instancia nueva, no sobre la original**, siempre que sea posible. Permite comparar y
   deja una vía de vuelta.

### 6.2 Restauración

```bash
# 1. Restaurar el base backup más reciente ANTERIOR al punto objetivo
#    (mecanismo del proveedor gestionado o pg_basebackup restaurado en el directorio de datos)

# 2. Configurar la recuperación hasta el punto objetivo
#    postgresql.conf / recovery settings:
#      restore_command      = '<comando de recuperación de segmentos WAL desde el archivo>'
#      recovery_target_time = '2026-09-09 18:42:00+00'
#      recovery_target_action = 'promote'

# 3. Arrancar la instancia y esperar a que termine la recuperación

# 4. Verificar antes de promover a producción (§6.3)
```

Restauración selectiva desde el backup lógico, cuando solo se necesita una tabla:

```bash
pg_restore --dbname="$EO_POSTGRES_URL" \
           --table=treaties --data-only --single-transaction \
           eo-logical-20260907T030000Z.dump
```

### 6.3 Verificación antes de dar el servicio por restaurado

Consultas mínimas. Si alguna no da lo esperado, **no se promueve**.

```sql
-- 1. El esquema está en la versión que espera el binario desplegado, y NO está dirty.
--    golang-migrate mantiene esta tabla con exactamente dos columnas: version y dirty.
SELECT version, dirty FROM schema_migrations;        -- esperado: dirty = false

-- 2. El mundo está completo: 256 chunks en el mundo MVP de 512x512 con chunks de 32x32
SELECT count(*) AS chunks FROM world_chunks;         -- esperado: 256

-- 3. world_state existe y su epoch_ms es coherente
SELECT * FROM world_state;

-- 4. Volumen de entidades en el orden de magnitud esperado
SELECT count(*) FROM players;
SELECT count(*) FROM cities;
SELECT count(*) FROM units;

-- 5. Invariante de movimiento: como máximo un movimiento ACTIVE por unidad
SELECT unit_id, count(*) AS active
FROM unit_movements
WHERE status = 'ACTIVE'
GROUP BY unit_id
HAVING count(*) > 1;                                  -- esperado: cero filas

-- 6. Movimientos activos con llegada ya vencida (se resolverán al arrancar; ver disaster-recovery.md)
SELECT count(*) FROM unit_movements
WHERE status = 'ACTIVE' AND arrival_time_ms <= (EXTRACT(EPOCH FROM now()) * 1000)::bigint;

-- 7. Referencias íntegras: ninguna unidad huérfana
SELECT count(*) FROM units u
LEFT JOIN players p ON p.id = u.player_id
WHERE p.id IS NULL;                                   -- esperado: 0
```

La consulta 5 comprueba `INV-MOVE-001` — una unidad tiene como máximo un movimiento `ACTIVE` —, que en la
base está materializado por un **índice único parcial**:

```sql
CREATE UNIQUE INDEX unit_movements_one_active_per_unit
    ON unit_movements (unit_id) WHERE status = 'ACTIVE';
```

Es decir: si la consulta 5 devuelve filas después de una restauración, algo muy raro ha pasado, porque el
propio índice debería haberlo impedido. Ejecutarla igualmente cuesta un segundo y confirma que el índice
sobrevivió a la restauración.

La consulta 6 no es un error: es información. Esos movimientos se completarán con *snap* al tile final
durante el arranque del servidor, y conviene saber cuántos son antes de arrancar. La cifra reaparecerá en el
log como `movements_arrived_while_down` en la línea `mundo rehidratado`: si no cuadra, el arranque hizo algo
distinto de lo previsto.

### 6.4 Vuelta al servicio

1. Apunta `EO_POSTGRES_URL` a la instancia restaurada.
2. Considera vaciar Redis (`FLUSHALL`): tras un PITR, las claves de idempotencia y de presencia pueden
   referirse a un futuro que ya no existe. Vaciarlo es seguro por lo explicado en §2, y evita
   inconsistencias sutiles.
3. Arranca el Game Server y verifica `/health` y `/ready`
   (ver [deployment.md](./deployment.md#paso-5--verificar)). Recuerda que el arranque **aplica las
   migraciones embebidas**: si el binario es más nuevo que el esquema restaurado, migrará hacia adelante
   sobre la copia recién restaurada. Confirma que eso es lo que quieres antes de arrancar.
4. Comprueba `eo_active_units`, `eo_active_movements`, `eo_connected_players` y la ausencia de
   `eo_protocol_errors_total{code="INTERNAL_ERROR"}`. Contrasta la línea de log `mundo rehidratado` con las
   cifras de la consulta 6 de §6.3.
5. Comunica el restablecimiento y **qué se perdió**, con honestidad y en unidades que el jugador entienda:
   "se han perdido las órdenes emitidas entre las 18:41 y las 18:42".

---

## 7. Backup lógico previo a migración destructiva

**Obligatorio, sin excepciones**, antes de cualquier migración que borre o transforme datos de forma no
reversible: eliminación de columna o tabla, cambio de tipo con pérdida, `CHECK` más estricto, borrado o
reescritura masiva de filas.

Por qué un backup lógico específico y no basta el PITR: el PITR restaura **todo el clúster** a un instante.
Si la migración corrió a las 03:00 y el problema se detecta a las 11:00, un PITR a las 02:59 tira ocho horas
de juego de todos los jugadores. Un volcado lógico de las tablas afectadas permite recuperar **solo** lo
dañado, sin tocar el resto del mundo.

```bash
# 1. Volcado de las tablas afectadas, etiquetado con la migración
pg_dump --format=custom --compress=9 \
        --table=<tabla_afectada> \
        --dbname="$EO_POSTGRES_URL" \
        --file="pre-migration-000014-add-territory-control-$(date -u +%Y%m%dT%H%M%SZ).dump"

# 2. Verificar que el volcado se puede leer ANTES de migrar
pg_restore --list "pre-migration-000014-....dump" | head -20

# 3. Subir la copia al almacenamiento externo y confirmar la subida

# 4. Solo entonces, desplegar la imagen que trae la migración.
#    NO hay un paso de migración separado: el arranque del binario las aplica
#    (ver deployment.md §6, Paso 3).
cd /opt/empires-online && docker compose up -d
```

Nombrado obligatorio: `pre-migration-<NNNNNN>-<nombre>-<timestamp>.dump`, con el mismo `NNNNNN` del archivo
`NNNNNN_nombre.up.sql` — seis dígitos, como `000001_initial_schema` y `000002_seed_catalogs`. Un backup que
no permite saber a qué migración corresponde es un backup que nadie usa bajo presión.

Y hay una razón extra para no saltarse este paso: como las migraciones se aplican **en el arranque**, el
punto de no retorno y el despliegue son el mismo evento. No existe la ventana de "he migrado, miro cómo ha
quedado y luego levanto la aplicación".

Retención: 90 días (§5.1).

La estrategia que **evita** llegar aquí es diseñar las migraciones en dos fases —aditiva primero,
destructiva en un despliegue posterior— de modo que el rollback sea un cambio de imagen. Ver
[deployment.md](./deployment.md#73-game-server-con-migraciones) y
[../database/migrations.md](../database/migrations.md).

---

## 8. Un backup no verificado no cuenta como backup

### 8.1 La regla

**Un backup que nunca se ha restaurado no cuenta como backup.** No es una frase motivacional: los modos de
fallo silenciosos son concretos y comunes.

- El proceso terminó con éxito pero escribió un archivo truncado.
- La clave de cifrado no es la que se creía y el archivo no se puede descifrar.
- El WAL se estaba archivando en un destino que se llenó hace tres semanas.
- El bucket externo dejó de aceptar escrituras tras una rotación de credenciales, y nadie miró.
- El backup existe y es válido, pero restaurarlo tarda cuatro horas y el RTO comprometido era una.

Ninguno de estos fallos aparece en el log del proceso de backup. Todos aparecen en el primer intento de
restauración.

### 8.2 Cadencia de las pruebas

| Prueba | Cadencia | Qué comprueba |
|---|---|---|
| Verificación automática de integridad | Cada backup | El archivo está completo y su suma de verificación cuadra |
| **Restauración completa a un entorno aislado** | **Mensual** | El procedimiento entero funciona y el RTO real es el comprometido |
| **Ensayo de PITR a un instante arbitrario** | **Trimestral** | El archivado de WAL funciona de verdad y el RPO se cumple |
| Restauración desde la copia **externa** | Trimestral | La copia fuera del proveedor principal es utilizable, con sus propias credenciales |
| Restauración selectiva de una tabla desde el volcado lógico | Trimestral | La vía de recuperación quirúrgica existe y se conoce |

### 8.3 Registro de la prueba

Cada ensayo se documenta con estos campos. Sin registro, la prueba no ocurrió.

| Campo | Ejemplo |
|---|---|
| Fecha y hora de inicio | 2026-10-01 09:00 UTC |
| Ejecutante | — |
| Backup usado (identificador y fecha) | `base-20260930T030000Z` |
| Punto objetivo | 2026-09-30 14:22:00 UTC |
| Entorno de destino | Instancia aislada, sin acceso a producción |
| **Duración real hasta base restaurada** | 18 min |
| **Duración real hasta `/ready` en 200** | 34 min |
| Verificaciones de §6.3 | Las 7, todas OK |
| Incidencias encontradas | — |
| ¿Se cumplió el RTO de 60 min? | Sí |
| ¿Se cumplió el RPO de 60 s? | Sí, pérdida medida: 41 s |

La columna de duración real es la que da valor al ejercicio: **el RTO comprometido debe basarse en tiempos
medidos, no estimados**. Si tres ensayos consecutivos superan los 60 minutos, el objetivo publicado es
falso y hay que corregir el procedimiento o el objetivo, no repetir el número.

El ensayo **nunca** se ejecuta contra producción ni con credenciales de producción.

### 8.4 Alertas del propio sistema de backup

El sistema de respaldo necesita su propia vigilancia; si no, su fallo es invisible hasta que se le necesita.

| Alerta | Condición | Severidad |
|---|---|---|
| Backup diario no completado | Sin base backup exitoso en 26 h | crítica |
| Archivado de WAL detenido | Sin segmento archivado en 10 min | crítica |
| Tamaño anómalo del backup | Desviación > 40 % respecto a la media de 7 días | aviso |
| Copia externa desactualizada | Sin copia secundaria en 48 h | crítica |
| Ensayo de restauración vencido | Sin ensayo registrado en 45 días | aviso |

Ver [monitoring.md](./monitoring.md#5-reglas-de-alerta-propuestas) para la convención de alertas del resto del sistema.

---

## Referencias

- [disaster-recovery.md](./disaster-recovery.md) — escenarios que consumen estos backups.
- [deployment.md](./deployment.md) — Postgres y Redis gestionados; migraciones.
- [configuration.md](./configuration.md) — `EO_POSTGRES_URL`, secretos y claves de cifrado.
- [monitoring.md](./monitoring.md) — alertas y verificación.
- [local-development.md](./local-development.md#61-postgres) — volcados en local vía `docker compose exec`.
- [../database/schema.md](../database/schema.md) — las 17 tablas respaldadas.
- [../invariants/README.md](../invariants/README.md) — invariantes verificados tras restaurar.
- [../decisions/ADR-003-postgresql-source-of-truth.md](../decisions/ADR-003-postgresql-source-of-truth.md) — por qué solo se respalda Postgres.
- [../decisions/ADR-004-redis-hot-state.md](../decisions/ADR-004-redis-hot-state.md) — por qué Redis no se respalda.

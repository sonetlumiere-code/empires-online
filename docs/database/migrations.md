# Migraciones de base de datos

Estrategia de evolución del esquema PostgreSQL: herramienta, reglas inmutables, patrón expand/migrate/contract, catálogo de migraciones del MVP y procedimiento de rollback.

El esquema resultante está descrito en [schema.md](./schema.md); los índices que algunas de estas migraciones crean, en [indexing.md](./indexing.md).

---

## 1. Herramienta y ubicación

**golang-migrate embebido en el binario del Game Server.** No hay una CLI separada que instalar, ni un contenedor de migraciones, ni un `Makefile` (en la máquina de desarrollo Windows `make` no está instalado; el task runner del proyecto son **pnpm scripts**).

| Aspecto | Decisión |
|---|---|
| Librería | `github.com/golang-migrate/migrate/v4` con el driver `pgx/v5` (registrado bajo el nombre `pgx5`) y la fuente `iofs` |
| Ubicación de los ficheros | `services/game-server/migrations/` |
| Nomenclatura | `NNNNNN_nombre.up.sql` y `NNNNNN_nombre.down.sql`, con **seis** dígitos |
| Embebido | `//go:embed *.sql` en `migrations/embed.go`, que es `package migrations` y vive junto al propio SQL |
| Tabla de versión | `schema_migrations` (`version bigint`, `dirty boolean`), creada y gestionada por golang-migrate |
| Conexión | `EO_POSTGRES_URL`, la misma que usa el servidor, con el esquema de la URL reescrito a `pgx5` |
| Aplicación | **Automática al arrancar el servidor, en todos los entornos**, antes de abrir puertos y de iniciar el game loop |

Embeber las migraciones en el binario elimina la clase entera de fallos "el binario y los ficheros SQL no coinciden": una versión del servidor lleva dentro exactamente las migraciones que espera. También evita depender de `psql`, que no está instalado en el entorno de desarrollo. Y el SQL vive en el mismo paquete que el `go:embed` para que exista **una sola copia**: duplicarlo en otra carpeta garantizaría que las dos versiones divergieran.

### 1.1 Fuente embebida

```go
// services/game-server/migrations/embed.go
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
```

```go
// internal/persistence/postgres/migrate.go
src, err := iofs.New(migrations.FS, ".")
...
dsn, err := toMigrateDSN(dbURL)       // postgres:// | postgresql:// | pgx:// -> pgx5://
...
m, err := migrate.NewWithSourceInstance("iofs", src, dsn)
...
before, dirty, verr := m.Version()
if dirty {
    return fmt.Errorf("el esquema está marcado como dirty en la versión %d: requiere intervención manual", before)
}
if err := m.Up(); err != nil {
    if errors.Is(err, migrate.ErrNoChange) {
        log.Info("esquema ya al día", "version", before)
        return nil
    }
    return fmt.Errorf("fallo al aplicar migraciones: %w", err)
}
```

Dos detalles con consecuencias operativas:

- **`migrate.ErrNoChange` no es un error**: significa que el esquema ya está al día. Tratarlo como fallo haría que cualquier reinicio normal aborte.
- **Un esquema `dirty` aborta el arranque**, sin intentar continuar. Que una migración anterior fallara a medias es un incidente que exige una persona; continuar automáticamente sería adivinar. Ver §7.2 y [../operations/disaster-recovery.md](../operations/disaster-recovery.md).

### 1.2 Cuándo se migra

Hoy, **siempre al arrancar**: `run()` llama a `postgres.Migrate` como paso 1, antes de abrir el pool, el puerto HTTP y el game loop. No hay distinción por `EO_ENV` ni subcomando `migrate`. Si la migración falla, el proceso termina con código distinto de cero: nunca se arranca con un esquema desconocido. En desarrollo eso hace que `pnpm run db:up && pnpm run server:run` deje una base lista sin pasos manuales.

Es una decisión adecuada a un despliegue de **una sola instancia**, que es el del MVP, y tiene un coste conocido que conviene tener escrito antes de que muerda:

- Varias réplicas arrancando a la vez competirían por el lock de migración. golang-migrate usa un advisory lock de PostgreSQL, así que no habría corrupción, pero sí arranques bloqueados y timeouts.
- Una migración lenta convierte un reinicio rutinario en una ventana de indisponibilidad no planificada.
- El patrón expand/migrate/contract (§3) exige poder decidir *cuándo* se migra respecto al despliegue del binario, y con migración automática esa decisión no existe.

Separar la migración del arranque —un subcomando `migrate up` / `migrate version` / `migrate down 1`, o un job de despliegue— es trabajo **pendiente**, no algo que el binario haga hoy. Mientras tanto, el procedimiento manual de §7.2 es la vía.

### 1.3 pnpm scripts

El task runner del proyecto son pnpm scripts. Los relevantes para la base:

```json
{
  "scripts": {
    "db:up":    "docker compose up -d postgres redis",
    "db:down":  "docker compose down",
    "db:reset": "docker compose down -v && docker compose up -d postgres redis",
    "db:logs":  "docker compose logs -f postgres redis",
    "db:psql":  "docker compose exec postgres psql -U empires -d empires",
    "db:redis": "docker compose exec redis redis-cli",
    "server:run": "cd services/game-server && go run ./cmd/server"
  }
}
```

No hay ningún script `db:migrate`: migrar es arrancar el servidor (§1.2). `db:reset` **borra los volúmenes** (`down -v`), así que recrea la base desde cero y vuelve a aplicar las dos migraciones en el siguiente arranque; es la vía rápida en desarrollo y no debe existir el reflejo de usarla en ningún otro sitio.

`db:psql` pasa por `docker compose exec` porque **`psql` no está instalado en la máquina de desarrollo**. Igual ocurre con `redis-cli`, de ahí `db:redis`. Nótese que la base se llama `empires`, no `empires_online`. Cualquier procedimiento de este documento que necesite una sesión SQL asume esa vía, y por tanto asume el daemon de Docker arrancado — que en el assessment del entorno estaba instalado pero apagado.

### 1.4 El frontend NO aplica migraciones

`apps/web` (el frontend Next.js) **todavía no existe** en el repositorio. Cuando exista, no tendrá credenciales de PostgreSQL, no importará ningún cliente SQL y no ejecutará migraciones. Es una regla de arquitectura, no una convención:

- El único escritor del esquema es el binario del Game Server. Un segundo escritor rompería la correspondencia entre versión de binario y versión de esquema.
- El frontend no es autoritativo sobre nada del mundo (principio 1 del canon). Darle acceso directo a la base contradiría eso incluso antes de escribir la primera consulta.
- `EO_POSTGRES_URL` nunca se expone al proceso del frontend ni, obviamente, al navegador. Los secretos van por variables de entorno del servicio que los necesita.

El frontend habla con el Game Server por WebSocket y con su propia API route de autenticación. Si algún día el frontend necesita almacenamiento propio (sesiones de usuario, por ejemplo), será una base o un esquema separado con su propio ciclo de migración: **TBD (fuera de MVP)**.

---

## 2. Reglas no negociables

### 2.1 Las migraciones publicadas son inmutables

Una migración es "publicada" en cuanto se mergea a la rama principal o se aplica en cualquier entorno compartido. A partir de ese instante, su contenido **no se edita jamás** — ni para arreglar un typo, ni para añadir una columna olvidada, ni para "limpiar" el SQL.

El motivo es mecánico: golang-migrate registra en `schema_migrations` sólo el número de versión, no un hash del contenido. Una base que ya aplicó `000001` no volverá a ejecutarla nunca. Editar `000001` produce dos bases con la misma `version` y esquemas distintos, y ese divergimiento es silencioso hasta que una consulta falla en producción y no en desarrollo.

Corolario: **todo arreglo es una migración nueva**. Si `000001_initial_schema.up.sql` olvidó una columna de `units`, no se toca: se escribe `000003_units_add_missing_column.up.sql`. La única excepción es una migración que aún no ha salido de la rama de trabajo del autor y no se ha aplicado en ningún entorno compartido.

### 2.2 Siempre se escribe el `.down.sql`

Cada `NNNNNN_nombre.up.sql` tiene su `NNNNNN_nombre.down.sql` que deshace exactamente sus efectos, en orden inverso. Sin excepciones, ni siquiera para migraciones "obviamente irreversibles". Las dos migraciones existentes cumplen la regla: `000001_initial_schema.down.sql` hace `DROP TABLE` de las diecisiete tablas en orden inverso al de dependencias y luego `DROP FUNCTION set_updated_at()`, y `000002_seed_catalogs.down.sql` borra las filas de semilla por su `code`.

El `down` cumple tres funciones, y sólo una de ellas es el rollback:

1. **Ciclo de desarrollo.** Permite `down 1` / `up` para reprobar una migración sin recrear la base entera.
2. **Tests de integración.** El CI puede aplicar todas las migraciones, revertirlas todas y volver a aplicarlas: si el `down` está mal, esto falla en CI y no en producción.
3. **Rollback real.** Sólo válido en los casos que §5 delimita.

Cuando el `down` no puede restaurar datos (por ejemplo, el `down` de un `DROP COLUMN` recrea la columna pero no su contenido), el fichero lo declara explícitamente en un comentario en su primera línea:

```sql
-- IRREVERSIBLE DATA LOSS: recrea la columna vacía; el contenido original no es recuperable
-- sin restaurar desde backup. Ver docs/database/migrations.md §5.
ALTER TABLE units ADD COLUMN legacy_field text;
```

### 2.3 Nada de DDL bloqueante prolongado sobre tablas grandes

Detallado en §4. Regla resumida: ninguna migración puede mantener un `ACCESS EXCLUSIVE` sobre `units`, `unit_movements` o `world_events` durante más de unos pocos milisegundos.

### 2.4 Los datos semilla de catálogo van en migraciones; el mundo, no

Los catálogos (`civilizations`, `factions`, `eras`) se siembran desde `000002_seed_catalogs.up.sql` porque otras tablas dependen de ellos por clave foránea: sin filas en `eras`, no se puede crear una ciudad. Las inserciones son idempotentes (`ON CONFLICT (id) DO NOTHING`, por `id` porque la semilla fija los identificadores) para que reaplicar sea inocuo.

La **generación del mundo** (`world_state` y las 256 filas de `world_chunks`) **no** es una migración. Es una tarea de bootstrap del servidor que lee `EO_WORLD_SEED`, `EO_WORLD_WIDTH`, `EO_WORLD_HEIGHT` y `EO_CHUNK_SIZE` y genera el terreno determinísticamente. Meterla en una migración congelaría valores de configuración dentro del esquema y ataría la forma del mundo al número de versión de la base. `000001` crea las dos tablas vacías; `WorldRepo.LoadOrInit` las puebla en el primer arranque y **falla** si el mundo persistido no coincide con la configuración, en vez de regenerar nada por su cuenta.

### 2.5 Una migración, un propósito

Cada fichero hace una cosa nombrable. No se agrupan cambios inconexos "porque van en el mismo PR". Facilita revisar, revertir y localizar el origen de un problema.

La excepción es la migración inicial: `000001_initial_schema` crea las diecisiete tablas de una vez porque el esquema fundacional **es** un solo propósito y porque sus claves foráneas son un grafo que no admite trocearse sin inventar un orden artificial. A partir de `000003`, la regla aplica sin matices.

---

## 3. Cambios compatibles hacia adelante: expand / migrate / contract

Todo cambio de esquema que afecte a código en ejecución se hace en **tres despliegues separados**. Esto permite desplegar sin downtime y, crucialmente, permite hacer rollback del *binario* sin hacer rollback del *esquema*.

```
Estado inicial:   binario vN          esquema S0
─────────────────────────────────────────────────────────────
Paso 1 EXPAND     binario vN          esquema S1  (S1 es compatible con vN y con vN+1)
                  ↑ el esquema crece; nada se rompe porque nada se quitó

Paso 2 MIGRATE    binario vN+1        esquema S1
                  ↑ el código nuevo empieza a usar lo nuevo; backfill de datos si aplica
                  ↑ AQUÍ el rollback a vN sigue funcionando: S1 soporta ambos

Paso 3 CONTRACT   binario vN+1        esquema S2  (S2 ya no soporta vN)
                  ↑ sólo cuando vN+1 lleva tiempo estable y no se piensa volver atrás
```

### 3.1 Ejemplo trabajado: renombrar `units.hp` a `units.health`

El cambio ingenuo (`ALTER TABLE units RENAME COLUMN hp TO health`) rompe el binario en ejecución en el instante exacto del `COMMIT`. La secuencia correcta:

**Expand** — `000010_units_add_health.up.sql`

```sql
ALTER TABLE units ADD COLUMN health integer;
UPDATE units SET health = hp WHERE health IS NULL;
```

El servidor sigue leyendo y escribiendo `hp`. La columna nueva existe pero nadie la usa.

**Migrate** — despliegue del binario que escribe **en las dos columnas** y lee de `health`, con fallback a `hp` si es `NULL`. En este punto un rollback del binario a la versión anterior funciona: `hp` sigue siendo correcta.

**Contract** — `000011_units_drop_hp.up.sql`, sólo después de confirmar que el binario nuevo es estable:

```sql
ALTER TABLE units DROP COLUMN hp;
```

Su `down` recrea la columna vacía y lleva el comentario de pérdida de datos de §2.2.

### 3.2 Añadir un valor a un enum de dominio

Los enums son `text` + `CHECK` precisamente para que esto sea barato. El caso vivo es `units.status`, cuyo `CHECK` real se llama `units_status_valid`; añadirle un estado nuevo se hace así:

```sql
-- 0000NN_units_status_add_fleeing.up.sql
ALTER TABLE units DROP CONSTRAINT units_status_valid;
ALTER TABLE units ADD CONSTRAINT units_status_valid
    CHECK (status IN ('IDLE', 'MOVING', 'GARRISONED', 'HIDDEN', 'DEAD', 'FLEEING')) NOT VALID;
ALTER TABLE units VALIDATE CONSTRAINT units_status_valid;
```

Nótese que **`units.unit_type` no tiene `CHECK`**: el catálogo de tipos de unidad vive en el dominio Go, así que añadir `SWORDSMAN` no requiere migración ninguna. La técnica de arriba aplica a los `CHECK` que sí existen: `units_status_valid`, `cities_presence_state_valid`, `unit_movements_status_valid`, `treaties_type_valid`, `treaties_status_valid`, `safe_zones_type_valid` y `territory_control_owner_type_valid`.

`NOT VALID` hace que el `ADD CONSTRAINT` no escanee la tabla: toma `ACCESS EXCLUSIVE` sólo para actualizar el catálogo. `VALIDATE CONSTRAINT` sí escanea, pero bajo `SHARE UPDATE EXCLUSIVE`, que **no bloquea** lecturas ni escrituras. Con un tipo `ENUM` de PostgreSQL esto habría requerido `ALTER TYPE ... ADD VALUE`, que hasta PostgreSQL 12 no podía ejecutarse dentro de una transacción y que no permite eliminar valores.

### 3.3 Añadir una columna `NOT NULL`

Nunca en un solo paso sobre una tabla grande. La secuencia sin bloqueo prolongado:

```sql
-- Expand (rápido: PostgreSQL 11+ no reescribe la tabla con un DEFAULT constante)
ALTER TABLE units ADD COLUMN morale integer NOT NULL DEFAULT 100;
```

Si el valor por defecto no es constante (una expresión volátil), el `ADD COLUMN` **sí** reescribe toda la tabla. En ese caso se separa: añadir la columna anulable sin default, backfill por lotes, y sólo entonces imponer la restricción:

```sql
ALTER TABLE units ADD COLUMN morale integer;
-- backfill por lotes desde la aplicación, no en la migración
ALTER TABLE units ADD CONSTRAINT units_morale_not_null CHECK (morale IS NOT NULL) NOT VALID;
ALTER TABLE units VALIDATE CONSTRAINT units_morale_not_null;
ALTER TABLE units ALTER COLUMN morale SET NOT NULL;  -- PG12+: usa el CHECK validado, no reescanea
```

Desde PostgreSQL 12, `SET NOT NULL` puede apoyarse en un `CHECK (col IS NOT NULL)` ya validado y saltarse el escaneo completo. Ese es exactamente el motivo de dar el rodeo.

---

## 4. `CREATE INDEX CONCURRENTLY` y la transacción implícita

### 4.1 Por qué no puede ir dentro de una transacción

Un `CREATE INDEX` normal toma un lock `SHARE` sobre la tabla: bloquea todas las escrituras mientras construye el índice. Sobre `units` con muchas filas, eso es una parada del game loop en la fase de persistencia y, por propagación, una acumulación en la cola de escritura (`eo_persistence_queue_depth` disparándose).

`CREATE INDEX CONCURRENTLY` evita el bloqueo, pero a cambio **no puede ejecutarse dentro de un bloque de transacción**. La razón es su algoritmo: hace dos pasadas completas sobre la tabla y, entre ellas, **espera a que terminen todas las transacciones que puedan ver una instantánea anterior**. Ese "esperar a otras transacciones" es incompatible con estar dentro de una transacción propia: una transacción tiene una única instantánea fija y no puede observar los commits de las demás mientras corre, así que el algoritmo nunca podría avanzar. PostgreSQL lo rechaza directamente:

```
ERROR: CREATE INDEX CONCURRENTLY cannot run inside a transaction block
```

### 4.2 Cómo se convive con golang-migrate

El driver `postgres` de golang-migrate envía el contenido completo del fichero `.sql` como **una sola cadena** al servidor. Cuando esa cadena contiene más de una sentencia, el protocolo simple de PostgreSQL la envuelve en una **transacción implícita** — y `CREATE INDEX CONCURRENTLY` falla con el error de arriba. Añadir `BEGIN`/`COMMIT` a mano no ayuda; quitarlos tampoco, porque la transacción implícita la crea el servidor.

La regla operativa, por tanto, es estricta:

> Un fichero de migración que contenga `CREATE INDEX CONCURRENTLY` (o `DROP INDEX CONCURRENTLY`) contiene **esa única sentencia y nada más**. Sin comentarios que generen sentencias, sin `SET`, sin una segunda instrucción.

Con una sola sentencia no hay transacción implícita y el comando se ejecuta correctamente. Si hacen falta tres índices concurrentes, son tres ficheros de migración consecutivos.

### 4.3 Índices concurrentes fallidos

`CREATE INDEX CONCURRENTLY` puede fallar a mitad y dejar un índice **inválido** (visible en `pg_index.indisvalid = false`). Ese índice no se usa para consultas pero sí penaliza las escrituras: hay que eliminarlo explícitamente.

```sql
SELECT c.relname
  FROM pg_index i
  JOIN pg_class c ON c.oid = i.indexrelid
 WHERE NOT i.indisvalid;
```

```sql
DROP INDEX CONCURRENTLY IF EXISTS units_chunk_idx;
```

El `down` de una migración de índice concurrente usa siempre `DROP INDEX CONCURRENTLY IF EXISTS`, también como sentencia única.

### 4.4 Índices en la creación inicial de la tabla

Cuando la tabla se crea vacía en la misma migración, **no** se usa `CONCURRENTLY`: no hay filas que escanear ni escrituras que bloquear, y el índice normal es más rápido y va en la misma transacción que el `CREATE TABLE`. Es exactamente lo que hace `000001`, que crea cada índice junto a su tabla dentro del mismo `BEGIN ... COMMIT`. `CONCURRENTLY` es exclusivamente para índices añadidos a tablas ya pobladas en un entorno vivo.

### 4.5 Timeouts defensivos

Toda migración que toque una tabla con datos empieza limitando cuánto puede esperar por un lock:

```sql
SET LOCAL lock_timeout = '3s';
SET LOCAL statement_timeout = '30s';
```

Sin `lock_timeout`, un `ALTER TABLE` que espera un lock se pone **en cola por delante** de todas las consultas posteriores, y una transacción larga de otro cliente convierte un cambio instantáneo en una parada total del servicio. Fallar rápido y reintentar es mucho mejor que bloquear la base. (Estas dos sentencias hacen que el fichero sea multi-sentencia: por eso nunca se combinan con `CREATE INDEX CONCURRENTLY`.)

---

## 5. Convención de numeración y nombres

```
NNNNNN_nombre_en_snake_case.up.sql
NNNNNN_nombre_en_snake_case.down.sql
```

| Elemento | Regla |
|---|---|
| `NNNNNN` | **Seis** dígitos con ceros a la izquierda, secuencial estricto desde `000001`. No timestamps |
| `nombre` | `snake_case` en **inglés**, descriptivo del efecto: `initial_schema`, `seed_catalogs`, `units_add_health` |
| Extensión | `.up.sql` y `.down.sql`. Ambos ficheros existen siempre, incluso si el `down` es un único comentario más el `DROP` correspondiente |

Los ficheros que existen hoy son, por tanto:

```
services/game-server/migrations/
├── 000001_initial_schema.up.sql / .down.sql
├── 000002_seed_catalogs.up.sql  / .down.sql
└── embed.go
```

**Secuencial en vez de timestamp** es una decisión consciente. Con timestamps, dos ramas concurrentes producen migraciones que se aplican en un orden distinto según cuándo se mergee cada una, y una base puede aplicar `20260901` después de `20260903`. Con numeración secuencial, dos ramas que añaden `000003` producen un **conflicto explícito** en el merge: alguien debe renumerar y, al hacerlo, decidir conscientemente el orden. El coste de ese conflicto es pequeño; el coste de un orden de aplicación ambiguo, no.

**Renumerar sólo antes de mergear.** Una vez en la rama principal, el número es inmutable (§2.1).

---

## 6. Migraciones del MVP

Son **dos**, y ambas están escritas y embebidas. Cada una materializa lo descrito en [schema.md](./schema.md).

| # | Fichero | Contenido |
|---|---|---|
| `000001` | `initial_schema` | Todo el esquema, dentro de un único `BEGIN ... COMMIT`: la función `set_updated_at()`; los catálogos `civilizations`, `factions`, `eras`; `players`; `world_state` y `world_chunks`; `cities`, `units`, `unit_movements`; `sessions` e `idempotency_keys`; `territories`, `territory_control`, `safe_zones`, `treaties`, `garrisons`; `world_events`. Además los seis triggers de `updated_at` y **todos** los índices, cada uno junto a su tabla (§4.4). `down`: `DROP TABLE` de las diecisiete tablas en orden inverso de dependencias más `DROP FUNCTION set_updated_at()` |
| `000002` | `seed_catalogs` | Semilla idempotente con `id` explícito: 4 civilizaciones (`ROMAN`, `BYZANTINE`, `PERSIAN`, `NORSE`, con sus `traits`), 3 facciones (`ORDER`, `CHAOS`, `NEUTRAL`), 4 eras (`STONE_AGE` 20, `BRONZE_AGE` 50, `IRON_AGE` 100, `CASTLE_AGE` 150, con `ordinal` 1–4). Separada de `000001` porque estructura y datos tienen ciclos de vida distintos: el esquema se corrige con una migración de esquema, el catálogo con una de datos. `down`: `DELETE` de esas filas por su `code` |

Por qué el esquema entero cabe en una sola migración y no en catorce: las claves foráneas forman un grafo (catálogos → `players` → `cities` → `units` → `unit_movements`), la base se crea vacía, y trocear el fichero sólo habría añadido catorce oportunidades de equivocarse con el orden sin ganar nada revisable. Los índices no se separan en una migración de "rutas calientes" por la misma razón: sobre una tabla vacía, un índice no es una optimización posterior sino parte de la definición — y el índice único parcial `unit_movements_one_active_per_unit` **debe** existir desde la primera fila, porque es la garantía física de `INV-MOVE-001`.

A partir de `000003`, cada cambio irá en su propio fichero con un único propósito (§2.5).

**Extensiones de PostgreSQL: ninguna.** Los `uuid` los genera la aplicación, no `gen_random_uuid()`, así que no se necesita ni `pgcrypto` ni ninguna función de núcleo concreta; y el "índice espacial" del MVP es un B-tree sobre `(chunk_x, chunk_y)`, así que no se necesita PostGIS. Cero extensiones significa que la imagen `postgres:16-alpine` del `docker-compose.yml` sirve tal cual y que un `pg_dump` restaura en cualquier instancia estándar.

---

## 7. Rollback y su relación con los backups

### 7.1 Jerarquía de respuestas ante una migración problemática

En orden de preferencia:

| Orden | Respuesta | Cuándo |
|---|---|---|
| 1 | **Forward fix**: nueva migración que corrige | Casi siempre. Es la única opción que no pierde datos escritos después de la migración defectuosa |
| 2 | **Rollback del binario, no del esquema** | Cuando el esquema es correcto y el fallo está en el código. Posible por diseño gracias a expand/migrate/contract (§3): el esquema tras un *expand* soporta ambas versiones del binario |
| 3 | **`migrate down`** | Sólo si la migración es puramente aditiva y estructural (crear tabla, crear índice) y aún no hay datos que dependan de ella. Típicamente en desarrollo y CI |
| 4 | **Restaurar desde backup** | Único camino cuando una migración de *contract* destruyó datos. Implica pérdida de todo lo escrito desde el backup |

### 7.2 Procedimiento de rollback

**El binario no expone hoy un subcomando `migrate` (§1.2).** Mientras eso siga así, el procedimiento es una sesión SQL por `pnpm run db:psql` y, si hace falta ejecutar un `down`, la CLI de golang-migrate apuntando a los mismos ficheros:

```powershell
# 1. Confirmar la versión actual y si hay estado sucio
pnpm run db:psql
```

```sql
SELECT version, dirty FROM schema_migrations;
```

Interpretación:

- **`dirty = false`.** El esquema está en un estado conocido. Si hay que retroceder un paso —y sólo en los casos del punto 3 de la tabla anterior—, se aplica a mano el `.down.sql` correspondiente y se ajusta `schema_migrations`, o se usa `migrate -path services/game-server/migrations -database <EO_POSTGRES_URL> down 1` con la CLI instalada ad hoc.
- **`dirty = true`.** La migración empezó y no terminó, y **el servidor se negará a arrancar** con ese mensaje: `el esquema está marcado como dirty en la versión N: requiere intervención manual`. No se ejecuta ningún `down` a ciegas. Se inspecciona qué quedó aplicado, se completa o se deshace a mano, y sólo entonces se limpia la marca.

Limpiar la marca **no ejecuta nada**: sobrescribe la fila de `schema_migrations`. Usarlo sin haber verificado el estado real del esquema es la forma más rápida de producir una base que dice estar en la versión N pero no lo está. La verificación se hace comparando el esquema real con [schema.md](./schema.md) y con el fichero de migración:

```sql
\d+ units
SELECT conname, pg_get_constraintdef(oid) FROM pg_constraint
 WHERE conrelid = 'units'::regclass;
```

En desarrollo hay una salida mucho más rápida y menos peligrosa que cualquier `force`: `pnpm run db:reset`, que destruye los volúmenes y deja que el siguiente arranque vuelva a aplicar `000001` y `000002` sobre una base limpia.

### 7.3 Backups y migraciones de contracción

**Regla: toda migración de contracción va precedida de un backup verificado.** Un `down` que recrea una columna vacía no es un rollback, es una cicatriz.

```powershell
# Antes de una migración de contract
docker compose exec -T postgres pg_dump -U empires -Fc empires > backup_pre_0000NN.dump
```

El backup se considera válido sólo si se ha restaurado con éxito en una base desechable. Un backup no verificado es una suposición. El detalle de la política de retención y del calendario de backups es responsabilidad de operaciones: **TBD (fuera de MVP)** en cuanto a frecuencia, destino y cifrado.

Relación con las tablas del esquema:

| Tabla | Consecuencia de perder datos | Reconstruible |
|---|---|---|
| `world_chunks`, `world_state` | Baja | **Sí**, regenerable desde `seed` — el mapa se regenera desde la semilla en cada arranque, y `world_chunks` es la copia de auditoría |
| `civilizations`, `factions`, `eras` | Baja | **Sí**, desde la migración `000002` |
| `sessions`, `idempotency_keys` | Baja | No, pero son efímeras por diseño (`expires_at`) y hoy ni siquiera se escriben |
| `players`, `cities`, `units` | **Crítica** | No |
| `unit_movements` (`ACTIVE`) | Alta | No; se pierden órdenes en curso |
| `world_events` | Media | No; es un log de auditoría |

Esa tabla es lo que determina qué se restaura primero y qué puede sacrificarse en una recuperación parcial.

### 7.4 Migraciones en CI

El job `integration` de `.github/workflows/ci.yml` levanta `postgres:16-alpine` y `redis:7-alpine` como services y ejecuta la suite etiquetada contra ellos:

```yaml
env:
  EO_INTEGRATION: '1'
  EO_TEST_POSTGRES_URL: postgres://empires:...@localhost:5432/empires_test?sslmode=disable
  EO_TEST_REDIS_URL: redis://localhost:6379/1
run: go test -race -count=1 -tags=integration ./...
```

La suite necesita **las dos condiciones**: la etiqueta de compilación `integration` y `EO_INTEGRATION=1`. Sin la variable, los tests se saltan solos, que es lo que hace que `pnpm run server:test:integration` no falle en una máquina sin bases de datos.

Esos tests aplican las migraciones sobre una base limpia antes de ejercitar los repositorios, de modo que un `up` roto se detecta ahí. El ciclo completo **`up` → `down` hasta 0 → `up`**, que es el que detecta `down` mal escritos y órdenes de `DROP` incorrectos respecto a las claves foráneas, todavía **no** está en el pipeline: añadirlo es trabajo pendiente y sería barato.

Aviso honesto: estos tests de integración están escritos pero **no se han ejecutado todavía en la máquina de desarrollo**, porque el daemon de Docker no arrancó. Lo verde hoy es la suite unitaria; la de integración está pendiente de un entorno con Docker en marcha.

---

## 8. Retención

Cuatro tablas crecen sin límite natural y necesitan una política. Ninguno de estos barridos está implementado todavía: son la política acordada, no código en ejecución.

| Tabla | Criterio | Mecanismo |
|---|---|---|
| `idempotency_keys` | `expires_at < now()` | Barrido periódico del servidor, **pendiente** —como la propia tabla, que aún no tiene escritor—. TTL 300 s, alineado con la clave `idem:{playerId}:{requestId}` de Redis |
| `sessions` | Antigüedad; `remote_addr` es dato personal | Borrado o anonimización por antigüedad. Plazo concreto: **TBD (fuera de MVP)** |
| `unit_movements` terminales | `status <> 'ACTIVE'` y antigüedad | Borrado por lotes. No afecta al índice único parcial, que sólo contiene filas `ACTIVE` |
| `world_events` | Antigüedad | Borrado por lotes o particionado por rango de `occurred_at`. El particionado es **TBD (fuera de MVP)**: se decide cuando el volumen lo justifique, y hacerlo entonces es una migración de expand sobre una tabla ya grande, que exige la secuencia de §3 |

El barrido se ejecuta **fuera del tick**, como cualquier otra I/O de PostgreSQL, y en lotes acotados: un `DELETE` masivo sobre `world_events` mantiene un lock largo y genera un pico de WAL que compite con las escrituras del juego.

---

## Documentos relacionados

- [schema.md](./schema.md) — el esquema que estas migraciones materializan
- [indexing.md](./indexing.md) — los índices creados por `000001` y su justificación
- [persistence-strategy.md](./persistence-strategy.md) — qué se escribe, cuándo y con qué garantías
- [../architecture/persistence.md](../architecture/persistence.md) — cola de persistencia y por qué el tick no hace I/O
- [../architecture/game-loop.md](../architecture/game-loop.md) — fases del tick
- [../decisions/ADR-012-database-migrations.md](../decisions/ADR-012-database-migrations.md) — la decisión de embeber las migraciones SQL
- [../operations/disaster-recovery.md](../operations/disaster-recovery.md) — qué hacer con un esquema `dirty` y cómo se restaura

# ADR-012: Migraciones SQL versionadas con golang-migrate embebidas en el binario

Propósito: fijar que el esquema de PostgreSQL evoluciona mediante ficheros SQL versionados gestionados con golang-migrate, embebidos en el binario del game server con `go:embed`, aplicados automáticamente en desarrollo y por comando explícito en producción, siendo el game server el único propietario del esquema.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-09-09** |
| Ámbito | `services/game-server/migrations`, `cmd/server`, `scripts/` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-003](ADR-003-postgresql-source-of-truth.md), [ADR-011](ADR-011-movement-timed-polyline.md) |

---

## 1. Contexto

PostgreSQL es la *source of truth* durable del proyecto. El esquema MVP incluye `players`, `civilizations`,
`factions`, `eras`, `cities`, `units`, `unit_movements`, `world_state`, `world_chunks`, `sessions`,
`idempotency_keys` y `schema_migrations`, más un conjunto de tablas creadas con lógica mínima o diferida
(`territories`, `territory_control`, `safe_zones`, `treaties`, `garrisons`, `world_events`). Su forma exacta
la fija [../database/schema.md](../database/schema.md).

Dos procesos distintos tocan ese mundo: el game server en Go y la aplicación Next.js. La pregunta que este
ADR responde no es solo «con qué herramienta migramos», sino sobre todo **quién es el dueño del esquema**,
porque la respuesta condiciona todo lo demás.

El entorno de desarrollo real impone además restricciones concretas: Windows 10, sin `make`, sin `psql`
instalado, con Docker CLI presente pero el daemon apagado por defecto. El task runner es **pnpm scripts**
([../operations/local-development.md](../operations/local-development.md)). Cualquier solución que exija
herramientas de línea de comandos adicionales instaladas a mano añade fricción real, no teórica.

El canon ya fija dos cosas que este ADR desarrolla: la convención de nombres
`NNNNNN_nombre.up.sql` / `.down.sql` en `services/game-server/migrations/`, y la existencia de la tabla
`schema_migrations`, que crea y mantiene golang-migrate.

---

## 2. Decisión

**El esquema evoluciona mediante ficheros SQL versionados en `services/game-server/migrations/`, gestionados
con golang-migrate, embebidos en el binario del game server mediante `go:embed`. En desarrollo se aplican
automáticamente al arrancar; en producción se aplican con un comando explícito, nunca en el arranque del
servidor. El game server es el único propietario del esquema.**

### 2.1 Ficheros y control de versión

Éstos son los ficheros que existen hoy, no un ejemplo:

```
services/game-server/migrations/
├── 000001_initial_schema.up.sql     todas las tablas, índices, CHECK y triggers del MVP
├── 000001_initial_schema.down.sql
├── 000002_seed_catalogs.up.sql      civilizations, factions y eras
├── 000002_seed_catalogs.down.sql
└── embed.go                         package migrations, //go:embed *.sql
```

El esquema completo cabe en una sola migración porque el proyecto aún no ha desplegado nada: mientras
`000001` no esté aplicada en ningún entorno del que dependa alguien, sigue siendo editable. En cuanto lo
esté, pasa a regirse por la regla 1 de §2.4 y todo cambio va en una migración nueva.

- Versión de seis dígitos con ceros a la izquierda, secuencial y sin huecos.
- **Todo `.up.sql` tiene su `.down.sql`.** Sin excepciones: una migración sin vuelta atrás es un despliegue
  sin plan de reversión.
- golang-migrate registra el estado en `schema_migrations` (columnas `version` y `dirty`), que es
  exactamente el nombre de tabla que el canon fija.

### 2.2 Embebido en el binario

```go
// services/game-server/migrations/embed.go
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
```

El directorio `migrations/` está **dentro** del módulo Go
(`github.com/empires-online/empires-online/services/game-server`), de modo que `go:embed` lo alcanza sin
trucos — a diferencia del JSON Schema del protocolo, que necesita un espejo dentro del módulo
([ADR-009](ADR-009-shared-protocol-package.md) §2.1). La fuente `iofs` de golang-migrate consume ese
`embed.FS` directamente: `iofs.New(migrations.FS, ".")`.

Un detalle que hay que conocer para no perder una tarde: **la URL de conexión se reescribe antes de
migrar**. `EO_POSTGRES_URL` llega con esquema `postgres://` o `postgresql://`, y el driver pgx/v5 de
golang-migrate se registra bajo el nombre `pgx5`; `toMigrateDSN` sustituye el esquema y rechaza cualquier
otro. Es una diferencia de esta herramienta, no del pool de la aplicación, que sigue usando la URL original.

Consecuencia inmediata: **el binario y su esquema viajan juntos**. No hay artefacto de migración separado
que pueda desincronizarse de la imagen, ni un paso de despliegue que copie ficheros `.sql` a alguna parte.

### 2.3 Cuándo se aplican

El comportamiento lo decide `EO_MIGRATE_ON_START`, cuyo valor por defecto **depende del entorno**: un
default único obligaría a recordar ponerlo justo en el sitio donde olvidarlo hace daño.

| Entorno | `EO_MIGRATE_ON_START` | Comportamiento |
|---|---|---|
| Desarrollo (`EO_ENV != production`) | `true` por defecto | `cmd/server/main.go` llama a `postgres.Migrate` como primer paso del arranque, antes de abrir el pool y de construir el mundo. Arrancar el servidor deja la base lista. |
| Integración / CI | `true` por defecto | El mismo arranque aplica las migraciones antes de ejecutar la suite de integración. |
| Producción (`EO_ENV=production`) | `false` por defecto | El servidor **no migra**: registra un `INFO` recordando que hay que ejecutar el binario `migrate`, y asume el esquema ya migrado. |

El binario dedicado es [`cmd/migrate`](../../services/game-server/cmd/migrate/main.go), y viaja **en la
misma imagen Docker que el servidor**. Eso es deliberado: hace imposible migrar con una versión del
esquema distinta de la que espera el binario que arrancará después.

```bash
migrate              # aplica las migraciones pendientes
migrate -version     # imprime la versión aplicada; sale con código 2 si el esquema quedó dirty
```

Lee `EO_POSTGRES_URL` del entorno, igual que el servidor —una única fuente de configuración evita
migrar por error una base distinta de la que se sirve— y también carga un `.env` si lo encuentra.

Los pnpm scripts equivalentes en la raíz son `pnpm run db:migrate` y `pnpm run db:version`.
`db:migrate:down` (revertir un escalón) y `db:migrate:verify` (up → down a cero → up contra una base
desechable) **siguen sin escribirse**: revertir se hace hoy invocando golang-migrate a mano.

**Por qué no se migra al arrancar en producción.** golang-migrate toma un *advisory lock* de PostgreSQL, así
que dos réplicas arrancando a la vez no se pisarían; el problema no es la carrera, es el acoplamiento. Migrar
en el arranque significa que el esquema cambia en el instante en que empieza el rollout, cuando todavía hay
procesos de la versión anterior sirviendo tráfico; y significa que revertir el despliegue no revierte el
esquema, dejando una situación ambigua sin dueño claro. Separar el paso hace explícito el orden y hace
posible parar entre migración y rollout si la migración se comporta mal.

### 2.4 Reglas de escritura de migraciones

| # | Regla | Motivo |
|---|---|---|
| 1 | **Lo publicado es inmutable.** Una migración fusionada en la rama principal no se edita jamás: se corrige con una migración nueva. | Editarla deja las bases ya migradas con un esquema distinto del que el fichero describe, y `schema_migrations` no lo detecta. |
| 2 | **Toda `up` tiene su `down`.** | Un despliegue sin reversión no es un despliegue, es una apuesta. |
| 3 | **Expand / migrate / contract** para cambios sobre tablas en uso. | Permite desplegar sin downtime y sin acoplar el orden entre esquema y código. |
| 4 | **Los índices se crean con `CREATE INDEX CONCURRENTLY`, y ese fichero contiene esa única sentencia.** | PostgreSQL envuelve implícitamente en una transacción todo bloque multi-sentencia enviado en una sola consulta, y `CREATE INDEX CONCURRENTLY` no puede ejecutarse dentro de una transacción. El `down` correspondiente usa `DROP INDEX CONCURRENTLY IF EXISTS`, también en solitario. |
| 5 | **DDL y backfills masivos van en ficheros separados.** | golang-migrate no tiene progreso ni reanudación: un backfill de millones de filas que falla a la mitad deja la migración `dirty` sin punto de retomada. |
| 6 | **Las migraciones que tocan tablas en uso fijan `lock_timeout`.** | `ALTER TABLE` toma `ACCESS EXCLUSIVE`; sin timeout, la migración espera detrás de una transacción larga y bloquea a todo el mundo tras de sí. |
| 7 | **Nada de renombrar columnas en un paso.** | Un rename rompe a cualquier versión del código que aún no se ha desplegado. Se hace con expand/migrate/contract. |

Ejemplo de la regla 6:

```sql
-- 000003_add_units_last_seen.up.sql
SET lock_timeout = '3s';
SET statement_timeout = '30s';

ALTER TABLE units ADD COLUMN last_seen_time_ms bigint;
```

### 2.5 Expand / migrate / contract, con un caso concreto

Añadir una columna obligatoria a `units` sin downtime, en tres despliegues:

```
Despliegue N            Despliegue N+1                  Despliegue N+2
────────────            ──────────────                  ──────────────
EXPAND                  MIGRATE                         CONTRACT
ALTER TABLE units       Backfill por lotes de las        ALTER TABLE units
  ADD COLUMN foo text;  filas antiguas (fichero            ALTER COLUMN foo
  (nullable)            de migración separado, o           SET NOT NULL;
                        tarea operativa).
El código nuevo         Todas las réplicas ya            Solo cuando ninguna
escribe foo; el         escriben foo.                    fila puede quedar NULL.
viejo lo ignora.
```

El orden es innegociable: nunca se contrae antes de que **todas** las instancias escriban la forma nueva. Un
`SET NOT NULL` prematuro convierte un despliegue rutinario en una caída total de escrituras.

---

## 3. Alternativas consideradas

### 3.1 Un ORM con migraciones automáticas (`AutoMigrate` y similares)

**A favor (real).** Fricción prácticamente nula durante las primeras semanas: el modelo Go *es* el esquema,
no hay ficheros que escribir ni que mantener sincronizados, y añadir un campo a una struct basta. Para un
prototipo desechable es difícil de superar en velocidad.

**En contra.** El DDL pasa a ser implícito y a estar generado por una librería: nadie revisa en el PR el SQL
que se va a ejecutar en producción, y no hay `down`. Los `AutoMigrate` típicos son deliberadamente
conservadores —añaden columnas pero no las eliminan ni las modifican de forma destructiva—, así que el
esquema real diverge lentamente del modelo sin que nada avise. Y no permiten expresar lo que este proyecto
necesita: `CREATE INDEX CONCURRENTLY`, expand/migrate/contract, `bigint GENERATED ALWAYS AS IDENTITY`, o los
enums de dominio como `text` + `CHECK` que el canon exige en lugar de tipos `ENUM` de PostgreSQL. Rechazada.

### 3.2 Prisma desde el lado de Next.js

**A favor (real).** Es la mejor experiencia de desarrollo de las cuatro opciones, sin discusión: `prisma migrate`
genera el SQL, lo versiona, detecta la deriva contra la base real y produce un cliente tipado excelente. El
equipo de frontend ya vive en TypeScript y el monorepo pnpm lo integraría sin esfuerzo. Y `prisma migrate diff`
es una herramienta genuinamente útil para revisar cambios.

**En contra.** Pone la propiedad del esquema en el lado equivocado. El dueño de la verdad del juego es el game
server: es quien define `unit_movements`, quien depende de que `world_chunks` sea `bytea` de 1024 bytes, y
quien sufre si un tipo cambia. Con Prisma, Go consumiría un esquema que no controla y cuya evolución decide
otro repositorio lógico y probablemente otra persona. **Dos dueños del esquema garantizan conflictos**: no es
un riesgo, es una cuestión de tiempo. Añade además el requisito de una *shadow database* para el flujo de
desarrollo de Prisma, es decir, una segunda base en el entorno local de un desarrollador Windows con el
daemon de Docker apagado. Rechazada.

### 3.3 Migraciones manuales

Alguien conecta a la base y ejecuta el SQL a mano, o lo pega en una consola.

**A favor (real).** Control total sobre lo que se ejecuta y cuándo, sin herramientas de por medio. En una
migración delicada, un operador experto que observa la base mientras aplica el cambio hace cosas que ningún
runner automático hace.

**En contra.** No es reproducible: no hay registro de qué se aplicó, ni garantía de que desarrollo,
integración y producción tengan el mismo esquema, ni forma de que CI levante una base limpia. Y en este
proyecto ni siquiera es practicable: `psql` no está instalado en la máquina de desarrollo. Rechazada como
mecanismo; se conserva como procedimiento excepcional de recuperación (§4.3).

### 3.4 Otras herramientas equivalentes (goose, dbmate, Atlas)

**A favor (real).** Son alternativas legítimas y en algún punto superiores. `goose` admite migraciones
escritas en Go, lo que permite backfills con lógica que en SQL puro sería retorcida — capacidad que
golang-migrate no tiene y que aquí se renuncia conscientemente. Atlas ofrece esquema declarativo con planes
de migración generados y verificación de deriva, que es más potente que un simple contador de versiones.

**En contra.** golang-migrate encaja con lo que el canon ya fijó sin adaptaciones: la convención
`NNNN_nombre.up.sql` / `.down.sql` es literalmente la suya, y la tabla `schema_migrations` es su valor por
defecto. Aporta *advisory lock*, fuente `iofs` para `go:embed` y un modelo mental que cabe en un párrafo.
Cambiar a otra herramienta más adelante es barato porque los ficheros son SQL plano. Se elige por encaje
directo, no por superioridad.

---

## 4. Consecuencias

### 4.1 Positivas

- **Propiedad del esquema inequívoca.** El game server es el único que define y aplica DDL. Cualquier
  discusión sobre la forma de una tabla tiene un único sitio donde resolverse.
- **El binario es autosuficiente.** La imagen Docker contiene el código y sus migraciones. No existe la
  posibilidad de desplegar una versión del servidor con las migraciones de otra.
- **CI puede levantar una base desde cero.** El pipeline arranca PostgreSQL, aplica las migraciones y
  ejecuta los tests de integración (`EO_INTEGRATION=1` y `go test -tags=integration ./...`, es decir
  `pnpm run server:test:integration`), sin depender de ningún volcado.
- **Revisable en el PR.** Las migraciones son SQL plano en el diff: alguien puede leer exactamente qué se va
  a ejecutar antes de aprobarlo, que es justo lo que un ORM con migraciones automáticas impide.
- **Reversibilidad real.** Cada `down` existe y `db:migrate:verify` demuestra en CI que funciona.

### 4.2 Negativas (enunciadas sin adornos)

- **El frontend nunca aplica migraciones y no puede asumir la forma del esquema.** `apps/web` consume la API
  HTTP o el protocolo WebSocket, no DDL. Esto es la consecuencia buscada, pero tiene un coste: cualquier dato
  que el frontend necesite y que hoy solo exista en una tabla obliga a añadir superficie de API en el game
  server en lugar de a escribir una consulta. Si en algún momento `apps/web` necesitara lectura directa de
  PostgreSQL, sería con un rol de solo lectura y sin permisos DDL; **cómo resuelve Next.js su autenticación
  contra `players` más allá de que no le corresponde migrar el esquema es TBD (fuera de MVP)**.
- **Un fallo deja `schema_migrations.dirty = true` y exige intervención humana.** `postgres.Migrate` lee la
  versión antes de hacer nada y, si el esquema está marcado `dirty`, **aborta el arranque** con el número de
  versión en el mensaje: el servidor no se levanta. Nadie continúa hasta que un operador inspeccione el
  estado real de la base, decida si la migración se aplicó parcialmente y haga `force` a la versión correcta.
  Es el comportamiento correcto —adivinar sería peor— pero significa que un fallo de migración **para el
  despliegue entero** hasta que alguien intervenga.
- **Revertir el binario no revierte el esquema.** Como las migraciones viajan dentro del binario, la versión
  antigua **no contiene** el `down` de las migraciones que la versión nueva introdujo, y por tanto no puede
  ejecutarlo. El orden de reversión es innegociable: primero `migrate down` con el binario **nuevo**, después
  desplegar el binario antiguo. Invertir el orden deja el esquema adelantado y sin herramienta para
  retrasarlo.
- **`down` es la parte menos probada de cualquier migración.** Se escribe una vez, se ejecuta casi nunca y
  suele estar mal. Por eso `db:migrate:verify` (up → down a cero → up) es un gate de CI y no una sugerencia;
  aun así, un `down` que se ejecuta correctamente sobre una base vacía puede fallar sobre una base con datos
  reales, y ese caso CI no lo cubre.
- **Un `down` de una migración destructiva no recupera los datos.** Revertir un `DROP COLUMN` recrea la
  columna vacía. La reversión del esquema no es la reversión de la información; para eso está la copia de
  seguridad ([../operations/backups.md](../operations/backups.md)).
- **No hay migraciones escritas en Go.** Un backfill que requiera lógica compleja hay que expresarlo en SQL o
  sacarlo del sistema de migraciones y convertirlo en una tarea operativa con su propio control de progreso.
- **La numeración secuencial produce conflictos en ramas paralelas.** Dos ramas que añaden `000003_` colisionan
  al fusionar. Es un conflicto ruidoso y visible —lo cual es preferible a un timestamp que se fusiona en
  silencio y se aplica en un orden distinto del que se probó— pero hay que renumerar a mano.

### 4.3 Procedimiento ante estado `dirty`

1. **No reintentar el despliegue.** Un segundo intento sobre un estado `dirty` no arregla nada.
2. Inspeccionar el esquema real por `docker compose exec` (no hay `psql` local) y determinar qué sentencias
   de la migración fallida llegaron a aplicarse.
3. Completar o deshacer manualmente hasta dejar la base en un estado que corresponda exactamente a una
   versión conocida.
4. `force` a esa versión para limpiar el flag.
5. Corregir la migración **con una migración nueva**, nunca editando la fallida (regla 1 de §2.4).

El runbook operativo vive en [../operations/deployment.md](../operations/deployment.md) y
[../operations/disaster-recovery.md](../operations/disaster-recovery.md).

---

## 5. Verificación

| Qué se verifica | Nivel | Cómo | Estado |
|---|---|---|---|
| Toda `up` tiene su `down` | revisión | Emparejamiento de ficheros y numeración sin huecos. Hoy se comprueba a ojo en el PR; automatizarlo en CI está pendiente. | Manual |
| Las migraciones aplican desde cero | integration | Base limpia con `pnpm run db:reset` y arranque del servidor, que migra. | Diseñado; pendiente de que arranque el daemon de Docker |
| Un esquema `dirty` detiene el arranque | integration | Marcar `schema_migrations.dirty` y comprobar que el servidor se niega a arrancar. | Diseñado; pendiente de Docker |
| Las migraciones revierten | CI | `db:migrate:verify`: up → down a cero → up. | Pendiente: el script no existe todavía (§2.3) |
| El esquema resultante coincide con la documentación | revisión | Diff contra [../database/schema.md](../database/schema.md) en el PR. | Manual |
| Índices concurrentes | revisión | Un fichero por índice concurrente, con una sola sentencia. | Manual |
| Migraciones inmutables | revisión | El diff de un PR no modifica ficheros de migración ya fusionados y aplicados. | Manual |

Los tests de integración necesitan PostgreSQL y Redis reales por Docker Compose. En la máquina de
desarrollo actual el daemon de Docker no llegó a arrancar, así que están **escritos pero no ejecutados**;
decir otra cosa sería mentir sobre el estado del proyecto.

---

## 6. Referencias

- [../database/migrations.md](../database/migrations.md) — convención de ficheros y operativa detallada.
- [../database/schema.md](../database/schema.md) — tablas canónicas y sus tipos.
- [../database/indexing.md](../database/indexing.md) — índices por patrón de acceso real.
- [../operations/local-development.md](../operations/local-development.md) — pnpm scripts y Docker Compose en Windows.
- [../operations/deployment.md](../operations/deployment.md) — orden de despliegue y migración.
- [../testing/integration-tests.md](../testing/integration-tests.md) — `EO_INTEGRATION=1` y el tag de build `integration`.

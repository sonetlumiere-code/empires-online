# Invariantes de Territorio y Safe Zones (INV-TERR-xxx, INV-SAFE-xxx)

Propiedades geométricas de las regiones del mundo, del control de territorio y del ocultamiento en zonas seguras.

Formato y severidades: [README.md](README.md). Geometría del mundo: [world.md](world.md). Estados de unidad: [units.md](units.md).

---

## Contexto: entidades creadas, mecánica diferida

Las tablas `territories`, `territory_control` y `safe_zones` existen en la migración `000001_initial_schema.up.sql` desde el primer día, pero **ninguna ruta de código Go las lee ni las escribe todavía**. Es una decisión deliberada del canon §11: crear la entidad en el MVP y diferir la mecánica evita una migración destructiva más adelante, cuando el modelo ya tenga datos reales encima.

Esta situación tiene una consecuencia directa sobre cómo hay que leer este archivo, y conviene no disimularla:

| Nivel | Estado real hoy |
|---|---|
| `DB` | **Vigente.** Las constraints de la migración están aplicadas y protegen los datos desde ya. |
| `DOMAIN` | **Pendiente.** No hay `SafeZoneIndex`, no hay `territoryOfTile`, no hay lógica de captura. |
| `BOUNDARY` | **Parcial.** `territory.update` existe en el catálogo de mensajes servidor→cliente y `world.snapshot` transporta `territories[]`; nada los emite todavía. |
| `TEST` | **Pendiente.** Ningún test de este archivo existe hoy. |

Cada ficha declara en su fila `Cobertura` qué parte está viva y qué parte es diseño. Un invariante cuya única garantía vigente es `DB` se declara así, no como si el dominio ya lo sostuviera.

### Geometría común

Territorios y safe zones comparten forma: un **rectángulo alineado a los ejes**, `[min_x, max_x] × [min_y, max_y]`, con ambos extremos incluidos. No son polígonos ni conjuntos arbitrarios de tiles, y esa elección es lo que hace baratos los invariantes de este archivo: comprobar el solape de dos rectángulos es una comparación de cuatro enteros, y construir el índice tile → región es un doble bucle.

```
   safe_zones / territories

   min_x                 max_x
     +---------------------+     min_y
     |                     |
     |     rectángulo      |
     |   alineado a ejes   |
     +---------------------+     max_y

   CHECK <tabla>_bounds_ordered:  min_x <= max_x AND min_y <= max_y
```

Las dos familias se documentan juntas porque comparten geometría, índice derivado y modo de fallo; se distinguen porque el territorio expresa **control** y la safe zone expresa **ocultamiento**.

---

<a id="inv-terr-001"></a>
## INV-TERR-001 — El rectángulo de un territorio es válido y cabe en el mundo

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | FAIL_FAST en la carga |
| Cobertura | **Parcial**: la mitad `DB` está vigente (`territories_bounds_ordered` aplicada en la migración); **no existe todavía** ningún test |

**Enunciado.** El rectángulo de un territorio cumple `min_x <= max_x` y `min_y <= max_y` (`CHECK territories_bounds_ordered`) y está contenido en `[0, EO_WORLD_WIDTH) × [0, EO_WORLD_HEIGHT)`.

**Razón.** Un rectángulo con los extremos invertidos no es un rectángulo pequeño: es un rectángulo **vacío**. El doble bucle `for y := min_y; y <= max_y; y++` no ejecuta ninguna iteración, así que el territorio existe como fila y no contiene ningún tile. No hay error, no hay log: simplemente el territorio no está en ninguna parte, y el jugador que intente capturarlo no encontrará nada que capturar.

La segunda mitad —contención en el mundo— tiene el modo de fallo contrario. Un territorio que se sale del mundo produce, al construir el índice tile → territorio, un acceso fuera del array; y al calcular su huella de chunks para emitir `territory.update`, un `chunkId` que no existe y un broadcast a suscriptores arbitrarios. Es el mismo problema que [INV-WORLD-001](world.md#inv-world-001) describe para una coordenada suelta, multiplicado por el área del rectángulo.

**Cómo se garantiza.**

- `DB` — `CONSTRAINT territories_bounds_ordered CHECK (min_x <= max_x AND min_y <= max_y)` en `000001_initial_schema.up.sql`. Es la garantía **vigente hoy**: un rectángulo invertido no puede existir en disco.
- `DOMAIN` — la contención en el mundo **no** es expresable como `CHECK`, por la misma razón que en [INV-WORLD-001](world.md#inv-world-001): `EO_WORLD_WIDTH` y `EO_WORLD_HEIGHT` son configuración, no esquema. Se validará al cargar los territorios en el arranque, comparando contra las dimensiones del mundo ya construido.
- `DOMAIN` — la validación de carga ocurre **antes** de construir el índice tile → territorio, para que el índice nunca se construya sobre una geometría que no cabe.

**Cómo se verifica.**

- Test previsto: `Test_INV_TERR_001_TerritoryBoundsValid` (integration) — un `INSERT` con `min_x > max_x` falla por `territories_bounds_ordered`; un territorio que se sale del mundo es rechazado por la carga, no por la base de datos.

**Violación en runtime.** Detección en el error de constraint y en la validación de carga. Log `invariant_violation` con `inv_id=INV-TERR-001`, `territory_id` y el rectángulo. Política `FAIL_FAST` de la carga de ese territorio: no se incorpora al índice. No se recorta el rectángulo al mundo: un territorio recortado en silencio cambia el mapa político sin que nadie lo decida.

---

<a id="inv-terr-002"></a>
## INV-TERR-002 — Dos territorios no comparten ningún tile

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | FAIL_FAST en la carga |
| Cobertura | **Sin cobertura**: la comprobación de solape es diseño pendiente; **no existe todavía** el índice ni el test |

**Enunciado.** Dos territorios no comparten ningún tile.

**Razón.** El índice derivado es una función `tile → territoryId`, y una función asigna **un** valor a cada entrada. Si dos rectángulos se solaparan, los tiles compartidos tendrían dos territorios, y toda pregunta sobre ellos —«¿quién controla este tile?», «¿a qué territorio pertenece esta unidad?»— tendría dos respuestas. La implementación tendría que elegir una, y cualquier criterio de elección (el id menor, el último cargado, el de menor área) es arbitrario y por tanto invisible para quien lea el código un año después.

Peor aún es el efecto sobre el control: dos territorios solapados con dueños distintos harían que el mismo tile estuviera simultáneamente en manos de dos jugadores. Cualquier regla que dependa de «estoy en territorio propio» daría respuestas contradictorias según por dónde se preguntara.

La propiedad es geométrica y no política: **también** se prohíbe el solape entre territorios del mismo dueño. Dos rectángulos que se tocan sin solaparse (aristas adyacentes) sí son válidos.

**Cómo se garantiza.**

- `DOMAIN` — el índice `territoryOfTile` se construye escribiendo cada tile una sola vez, en orden ascendente de `id`; una segunda escritura sobre un tile ya asignado **es** la detección del solape. `territory.BuildSet` no sobrescribe: el `id` menor conserva el tile y el conflicto se devuelve al llamante con el primer tile afectado y el total de tiles compartidos.
- No hace falta además una comparación por pares: pintar el índice ya recorre todos los tiles, y comparar rectángulos sería trabajo duplicado que puede divergir del resultado real del índice.
- `DB` — **no hay garantía en la base de datos**. PostgreSQL puede expresar exclusión de rangos con `EXCLUDE USING gist`, pero la migración `000001` **no** la declara: la tabla `territories` sólo tiene `territories_bounds_ordered`. Añadirla exigiría la extensión `btree_gist` y una migración: **TBD (fuera de MVP)**.

**Cómo se verifica.**

- `TestDosTerritoriosSolapadosResuelvenElIDMenorYSeReportan` y `TestElReporteDeSolapamientosEsDeterminista` (unit, `internal/domain/territory/territory_test.go`): el solape se detecta, gana el `id` menor, se cuentan los tiles compartidos, y el informe no depende del orden de entrada.
- `TestLaRejillaCubreElMundoSinHuecosNiSolapes` (unit): la rejilla que siembra el mundo no se solapa consigo misma, que es el único productor de geometría del MVP.

**Violación en runtime.** Log de nivel `error` por cada par en conflicto, con los dos `territory_id`, el primer tile afectado y el total de tiles compartidos, y **el servidor arranca igualmente** con el estado marcado como inconsistente.

**Esto NO es `FAIL_FAST`, y es deliberado.** Una versión anterior de este documento pedía abortar el arranque. Se descartó: negarse a arrancar por unos rectángulos sembrados mal deja el mundo entero inaccesible para todos los jugadores, mientras que el modo degradado es *determinista* —gana siempre el `id` menor, el mismo en cada arranque— y afecta sólo a los tiles en conflicto. Un mapa político ambiguo es peor que uno correcto y mucho mejor que ninguno. La regla vinculante es `RN-TERR-004` de [../specs/territory.md](../specs/territory.md).

---

<a id="inv-terr-003"></a>
## INV-TERR-003 — Una fila de control por territorio, y siempre con territorio

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, TEST |
| Milestone | M7 |
| Política ante violación | FAIL_FAST (constraint) |
| Cobertura | **Parcial**: la mitad `DB` está vigente (PK y FK de `territory_control` aplicadas); **no existe todavía** ningún test |

**Enunciado.** Todo territorio tiene como máximo una fila en `territory_control` y toda fila de control referencia un territorio existente (`territory_id` es `PRIMARY KEY` y `FOREIGN KEY ON DELETE CASCADE`).

**Razón.** Separar la **geometría** (`territories`) del **control** (`territory_control`) es una decisión deliberada del esquema, y buena: permite introducir después ownership de clan, influencia de facción, estado disputado y tiempo de captura sin migrar destructivamente la tabla de geometría, que es la que tendrá datos que nadie quiere perder.

El precio de esa separación es que ahora hay dos tablas que pueden desincronizarse, y este invariante es lo que lo impide en las dos direcciones:

- **Dos filas de control para un territorio** haría ambigua la pregunta «¿quién lo controla?», exactamente como el solape de [INV-TERR-002](#inv-terr-002) hace ambigua «¿de qué territorio es este tile?».
- **Una fila de control huérfana** —sin territorio— es control sobre una región que no existe: no tiene tiles, no se puede emitir a ningún chunk y nadie puede disputarla. Se acumularía en silencio cada vez que se borrara un territorio.

Que la relación sea 0..1 y no 1..1 es intencional: un territorio recién creado y nunca disputado **no necesita** fila de control. La ausencia de fila y `owner_type = 'NONE'` significan lo mismo, y no tener que insertar una fila vacía por cada territorio del mapa es un ahorro real.

**Cómo se garantiza.**

- `DB` — `territory_control.territory_id bigint PRIMARY KEY REFERENCES territories (id) ON DELETE CASCADE`. Una sola columna hace las dos mitades: la `PRIMARY KEY` impone «como máximo una fila por territorio» y la `FOREIGN KEY` impone «siempre con territorio». Es la garantía **vigente hoy**.
- `DB` — `ON DELETE CASCADE` es lo que impide las filas huérfanas sin necesidad de ninguna limpieza periódica: borrar el territorio borra su control.
- `DB` — el trigger `territory_control_set_updated_at` mantiene `updated_at`, de modo que un cambio de control es auditable en el tiempo sin código adicional.

**Cómo se verifica.**

- Test previsto: `Test_INV_TERR_003_OneControlRowPerTerritory` (integration) — un segundo `INSERT` para el mismo `territory_id` falla por clave primaria; un `INSERT` con un `territory_id` inexistente falla por clave foránea; borrar el territorio borra su fila de control.

**Violación en runtime.** Imposible en escritura: PostgreSQL rechaza ambos casos. En lectura, una fila de control cuyo territorio no se pudo cargar produce log `invariant_violation` con `inv_id=INV-TERR-003` y `territory_id`. Política `FAIL_FAST` de la carga de ese control: no se incorpora al índice.

---

<a id="inv-terr-004"></a>
## INV-TERR-004 — `owner_type` y `owner_id` son consistentes

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, TEST |
| Milestone | M7 |
| Política ante violación | FAIL_FAST (constraint) |
| Cobertura | **Parcial**: la mitad `DB` está vigente (`territory_control_owner_consistency` aplicada); **no existe todavía** ningún test |

**Enunciado.** `(owner_type = 'NONE') = (owner_id IS NULL)`, garantizado por `CHECK territory_control_owner_consistency`.

**Razón.** Son dos columnas que codifican **un solo hecho** —quién controla el territorio— y las dos combinaciones incoherentes son ambas peligrosas, cada una a su manera:

- **`owner_type = 'NONE'` con `owner_id` no nulo** deja un identificador de dueño en una fila que dice no tener dueño. Cualquier consulta escrita como «dame el `owner_id` de los territorios controlados» lo excluirá correctamente, pero una escrita como «dame el `owner_id` donde no sea nulo» lo incluirá: dos consultas razonables, dos respuestas distintas sobre el mismo dato.
- **`owner_type <> 'NONE'` con `owner_id` nulo** es peor: el territorio afirma tener dueño y no dice quién. La comprobación «¿es mío este territorio?» comparará contra `NULL`, y en SQL esa comparación no es falsa sino **desconocida**, con lo que la fila desaparece de los dos lados de cualquier partición `WHERE owner_id = $1` / `WHERE owner_id <> $1`. Un territorio invisible para ambos bandos.

La severidad es `CRITICO` y no `ALTO` porque el control de territorio es, en cuanto exista la mecánica, la base sobre la que se decidirán acciones hostiles: un dueño ambiguo es una autorización ambigua.

**Cómo se garantiza.**

- `DB` — `CONSTRAINT territory_control_owner_consistency CHECK ((owner_type = 'NONE' AND owner_id IS NULL) OR (owner_type <> 'NONE' AND owner_id IS NOT NULL))`. La bicondicional está escrita como la disyunción de sus dos casos, que es la forma expresable en SQL. Es la garantía **vigente hoy**.
- `DB` — `CONSTRAINT territory_control_owner_type_valid CHECK (owner_type IN ('NONE', 'PLAYER', 'CLAN', 'FACTION'))` cierra el dominio del discriminador, en línea con la convención de `text` + `CHECK` del canon §11. Sin esa segunda constraint, un `owner_type` inventado pasaría por la rama «distinto de NONE» y exigiría un `owner_id` sin que nada supiera interpretarlo.
- `DB` — `owner_type text NOT NULL DEFAULT 'NONE'`: una fila de control recién creada nace sin dueño y consistente, sin que el código tenga que acordarse de inicializar nada.

**Cómo se verifica.**

- Test previsto: `Test_INV_TERR_004_OwnerTypeIdConsistency` (integration) — las cuatro combinaciones: `('NONE', NULL)` y `('PLAYER', id)` se aceptan; `('NONE', id)` y `('PLAYER', NULL)` fallan por `territory_control_owner_consistency`.

**Violación en runtime.** Imposible en escritura. En lectura, una combinación incoherente sólo puede venir de una migración mal hecha: log `invariant_violation` con `inv_id=INV-TERR-004`, `territory_id`, `owner_type` y `owner_id`. Política `FAIL_FAST` de la carga: el territorio se incorpora **sin** control, nunca con un dueño adivinado.

---

<a id="inv-terr-005"></a>
## INV-TERR-005 — Un dueño de tipo `PLAYER` existe

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REPAIR (liberar el territorio) |
| Cobertura | **Sin cobertura**: no hay clave foránea que lo garantice ni validación de carga todavía |

**Enunciado.** Si `owner_type = 'PLAYER'`, `owner_id` es un `players.id` existente. No hay clave foránea: `owner_id` es `text` polimórfico.

**Razón.** `owner_id` es `text` y no `uuid` porque debe poder referenciar tres cosas distintas según el discriminador: un jugador (`uuid`), un clan o una facción (identificadores que hoy no existen). Ese polimorfismo es lo que permite que el modelo crezca sin migrar la tabla, y su precio es exacto y hay que nombrarlo: **PostgreSQL no puede garantizar la integridad referencial de una columna polimórfica**. No hay `FOREIGN KEY` posible hacia «una de tres tablas según el valor de otra columna».

La consecuencia es concreta: si un jugador se borra, sus territorios quedan apuntando a un `uuid` que ya no existe. `players` tiene `ON DELETE CASCADE` hacia `cities` y `units`, pero **no puede tenerlo hacia `territory_control`**, porque la columna no la referencia. Un territorio con dueño fantasma es intocable: nadie puede capturarlo, porque la regla de captura preguntará por un jugador que no está.

**Cómo se garantiza.**

- `DOMAIN` — la validación de carga resolverá cada `owner_id` de tipo `PLAYER` contra los jugadores ya cargados. Es el sustituto de la clave foránea, y tiene que ser explícito precisamente porque la base de datos no lo hace.
- `DOMAIN` — el cambio de control escribirá `owner_id` a partir del `playerId` de la sesión, que proviene del ticket verificado ([INV-PLAYER-004](player.md#inv-player-004)) y por tanto existe: no de un valor del payload.
- `DOMAIN` — el borrado de un jugador deberá liberar sus territorios (`owner_type = 'NONE'`, `owner_id = NULL`) en la misma transacción. Mientras esa ruta no exista, la validación de carga es la única defensa.

**Cómo se verifica.**

- Test previsto: `Test_INV_TERR_005_PlayerOwnerExists` (integration) — barrido: todo `owner_id` con `owner_type = 'PLAYER'` resuelve en `players`; borrar un jugador con territorios y recargar deja esos territorios sin dueño, no con un dueño fantasma.

**Violación en runtime.** Detección en la validación de carga. Log `invariant_violation` con `inv_id=INV-TERR-005`, `territory_id` y el `owner_id` irresoluble. Política `REPAIR`: el territorio se libera (`owner_type = 'NONE'`, `owner_id = NULL`) y se registra un `world_events` de auditoría. Liberar es la reparación correcta porque devuelve el territorio al juego; conservar un dueño inexistente lo saca de él para siempre.

---

<a id="inv-terr-006"></a>
## INV-TERR-006 — En MVP no hay progreso de captura

| Campo | Valor |
|---|---|
| Severidad | MEDIO |
| Aplicación | TEST |
| Milestone | M7 |
| Política ante violación | Log `error`; sin reparación |
| Cobertura | **Sin cobertura**: se cumple trivialmente porque nada escribe `territory_control`; **no existe todavía** el test que lo fije |

**Enunciado.** En MVP, `control_points = 0` y `contested = false` para toda fila de `territory_control`.

**Razón.** Es un invariante de **alcance**, no de corrección: declara explícitamente qué parte del modelo está dormida. Las columnas `control_points` y `contested` existen para una mecánica de captura progresiva que el MVP no implementa, y su valor por defecto (`0` y `false`) es el único coherente con esa ausencia.

Documentarlo como invariante tiene un propósito práctico: si algún día aparece un `control_points` distinto de cero sin que nadie haya implementado la captura, es la señal de que algo está escribiendo esas columnas por accidente —una migración, un script de datos, un endpoint de pruebas— y eso hay que verlo. Un valor inesperado en una columna dormida es más informativo que un valor inesperado en una columna viva, porque no puede tener ninguna explicación legítima.

Su única garantía es `TEST`, y así se declara: es un invariante frágil por definición, y la ficha lo dice en lugar de fingir una defensa que no existe.

**Cómo se garantiza.**

- `TEST` — un barrido de integración comprueba el estado de las dos columnas. No hay ninguna otra defensa, y no debe haberla: poner un `CHECK (control_points = 0)` obligaría a una migración el día que se implemente la captura, para eliminar una constraint que sólo existía para documentar.
- `DB` — lo que sí está garantizado es el rango: `control_points integer NOT NULL DEFAULT 0 CHECK (control_points >= 0)`. Un progreso de captura negativo es imposible incluso cuando la mecánica exista.
- `DOMAIN` — ninguna ruta de código escribe `territory_control` en MVP, así que la propiedad se cumple trivialmente. Ésa es la razón de que la ficha declare `MEDIO` y no algo más alto.

**Cómo se verifica.**

- Test previsto: `Test_INV_TERR_006_NoCaptureProgressInMVP` (integration) — barrido sobre `territory_control`: `control_points = 0` y `contested = false` en todas las filas.

**Violación en runtime.** Detección en el test de integración. Log `invariant_violation` con `inv_id=INV-TERR-006` y `territory_id`. Sin reparación automática: el valor se conserva y se investiga de dónde salió. Cuando exista la mecánica de captura, este invariante se marcará `DEROGADO` con su ADR, y su número quedará quemado según la convención de [README.md](README.md) §2.

---

<a id="inv-terr-007"></a>
## INV-TERR-007 — Un territorio con dueño tiene fecha de captura

| Campo | Valor |
|---|---|
| Severidad | MEDIO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REPAIR (fijar `captured_at`) |
| Cobertura | **Sin cobertura**: no hay ruta que escriba `territory_control` todavía |

**Enunciado.** `owner_type <> 'NONE'` implica `captured_at IS NOT NULL`.

**Razón.** `captured_at` es el dato que convierte un hecho en un hecho **fechado**, y de él dependen tres cosas que el modelo ya prevé: la antigüedad de la posesión, cualquier periodo de gracia tras una captura, y la reconstrucción del historial político desde `world_events`. Un territorio con dueño y sin fecha es un dueño sin origen: no se puede saber si lo tomó hace un minuto o hace un mes, y ninguna regla que dependa del tiempo de posesión puede evaluarse.

Es el mismo patrón que [INV-CITY-011](city.md#inv-city-011) —un estado que exige la marca temporal que lo originó— y falla igual: no con un error, sino con una regla que no se puede aplicar.

La implicación es deliberadamente **en un solo sentido**. Un territorio liberado (`owner_type = 'NONE'`) puede conservar su `captured_at` como dato histórico, y eso es útil: dice cuándo fue capturado por última vez. Exigir que se borrara al liberar destruiría información sin ganar nada.

**Cómo se garantiza.**

- `DOMAIN` — el cambio de control escribirá `owner_type`, `owner_id` y `captured_at` en la misma sentencia, con el `now` del `Clock` inyectado. No habrá ninguna ruta que fije el dueño sin la fecha.
- `DOMAIN` — la validación de carga comprobará la implicación antes de incorporar el control al índice.
- `DB` — **no hay `CHECK`** que lo imponga, aunque sería expresable. La migración `000001` no lo declara, y esta ficha no lo inventa: la garantía es de dominio y de test.

**Cómo se verifica.**

- Test previsto: `Test_INV_TERR_007_OwnedTerritoryHasCapturedAt` (integration) — barrido: ninguna fila con `owner_type <> 'NONE'` tiene `captured_at IS NULL`; una fila liberada con `captured_at` no nulo se acepta como control negativo.

**Violación en runtime.** Detección en la validación de carga. Log `invariant_violation` con `inv_id=INV-TERR-007` y `territory_id`. Política `REPAIR`: se fija `captured_at` al instante de la detección y se registra un `world_events`. Es una reparación conservadora —hace la posesión más reciente de lo que fue, nunca más antigua—, que es el lado seguro si alguna regla futura premia la antigüedad.

---

<a id="inv-terr-008"></a>
## INV-TERR-008 — El índice tile → territorio es determinista

| Campo | Valor |
|---|---|
| Severidad | MEDIO |
| Aplicación | TEST |
| Milestone | M7 |
| Política ante violación | Log `error` + métrica |
| Cobertura | **Sin cobertura**: el índice no existe todavía |

**Enunciado.** El índice `territoryOfTile` es función pura de `territories`: reconstruirlo produce el mismo resultado byte a byte.

**Razón.** Es la misma disciplina que [INV-WORLD-005](world.md#inv-world-005) aplica al generador de mundo, trasladada a un índice derivado. Un índice reconstruible y determinista se puede tirar y rehacer en cualquier momento —al arrancar, tras una reparación, en un test— con la certeza de obtener exactamente lo mismo. Uno no determinista deja de ser derivado y se convierte, de facto, en estado propio que habría que persistir y versionar.

Las fuentes de no-determinismo que este invariante prohíbe son las habituales y todas evitables: iterar un `map` de Go sin ordenar (el orden de iteración es aleatorio por diseño del runtime), construir en paralelo con escrituras concurrentes al mismo buffer, y depender del orden en que la base de datos devolvió las filas sin un `ORDER BY` explícito.

Con [INV-TERR-002](#inv-terr-002) vigente —sin solapes— el determinismo es casi gratuito: si cada tile pertenece como mucho a un territorio, el orden de construcción **no puede** cambiar el resultado. Ambos invariantes se sostienen mutuamente, y por eso este es `MEDIO`: su violación indicaría casi con seguridad que el otro también está roto.

**Cómo se garantiza.**

- `DOMAIN` — la construcción recorrerá los territorios en orden explícito de `id` y, dentro de cada uno, el rectángulo en doble bucle `y` externo, `x` interno. Nunca una iteración de `map` ni una goroutine por territorio.
- `DOMAIN` — la consulta de carga llevará `ORDER BY id` explícito: el orden que devuelve PostgreSQL sin cláusula de orden no está garantizado.
- `DOMAIN` — el índice es un array plano indexado por `y*width + x`, la misma disposición fila-mayor que el terreno: sin estructuras cuyo recorrido dependa de hashes.

**Cómo se verifica.**

- Test previsto: `Test_INV_TERR_008_TerritoryIndexIsDeterministic` (unit) — construir el índice dos veces desde el mismo conjunto de territorios y comparar con `bytes.Equal`; repetir barajando el orden de entrada para comprobar que el resultado no depende de él.

**Violación en runtime.** Detección en el test de reconstrucción. Log `invariant_violation` con `inv_id=INV-TERR-008`. Sin reparación: se conserva el índice existente y se investiga. Un índice no determinista no corrompe estado durable, pero invalida cualquier test que compare huellas.

---

<a id="inv-terr-009"></a>
## INV-TERR-009 — Todo cambio de control se emite a los chunks del territorio

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | BOUNDARY, TEST |
| Milestone | M7 |
| Política ante violación | REPAIR (reemitir) |
| Cobertura | **Parcial**: `territory.update` existe en el catálogo de mensajes servidor→cliente y `world.snapshot` transporta `territories[]`; **nada los emite todavía** |

**Enunciado.** Ningún cambio de `territory_control` ocurre sin emitir `territory.update` a la huella de chunks del territorio.

**Razón.** El cliente no consulta el estado: lo recibe. Su modelo del mundo es el `world.snapshot` inicial más la secuencia de deltas posteriores, así que un cambio de control que no se emite **no existe para nadie** hasta la siguiente reconexión. El jugador que acaba de capturar un territorio no ve que lo ha capturado, y el que lo ha perdido sigue viéndolo suyo: dos clientes con dos mapas políticos distintos, ambos equivocados, y sin ningún error que lo delate.

La cualificación «a la huella de chunks del territorio» es la parte con contenido técnico. Un territorio abarca un rectángulo que puede cruzar varios chunks, y el interest management suscribe **por chunk** ([INV-WORLD-006](world.md#inv-world-006)). Emitir sólo al chunk del centro dejaría a oscuras a quien esté mirando un borde. La huella correcta es el conjunto de chunks que intersecan el rectángulo: desde `ChunkOf(min_x, min_y)` hasta `ChunkOf(max_x, max_y)`, ambos incluidos.

**Cómo se garantiza.**

- `BOUNDARY` — el cambio de control y la emisión ocurrirán en el mismo punto de código, no en dos pasos que alguien pueda separar en un refactor. El patrón es el mismo que ya usa `Simulation.applyPresence` para `city.update`: mutar, encolar la escritura y emitir el delta, las tres cosas juntas.
- `BOUNDARY` — `territory.update` es uno de los 14 mensajes servidor→cliente de la v1, y `world.snapshot.payload` ya transporta `territories[]`: el contrato existe y no hay que ampliarlo, sólo emitirlo.
- `BOUNDARY` — la huella se calculará con `World.ChunkOf` sobre las dos esquinas del rectángulo, y el broadcast irá a cada chunk de ese rango. Reutilizar la única implementación de la conversión es lo que evita que la huella diverja del criterio de suscripción.
- `BOUNDARY` — los esquemas servidor→cliente **no** son estrictos, así que añadir campos opcionales a `territory.update` en el futuro no romperá clientes antiguos.

**Cómo se verifica.**

- Test previsto: `Test_INV_TERR_009_ControlChangeEmitsTerritoryUpdate` (integration) — un observador suscrito a cada chunk de la huella recibe exactamente un `territory.update` por cambio de control; un observador suscrito a un chunk fuera de la huella no recibe ninguno.

**Violación en runtime.** Detección en una aserción posterior al cambio de control que compara el número de broadcasts con el tamaño de la huella. Log `invariant_violation` con `inv_id=INV-TERR-009`, `territory_id` y los chunks omitidos. Política `REPAIR`: se reemite a los chunks que faltaban. La reemisión es segura porque `territory.update` transporta el estado completo del control, no un incremento: recibirlo dos veces es idempotente.

---

<a id="inv-safe-001"></a>
## INV-SAFE-001 — Dos Safe Zones no comparten ningún tile

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | FAIL_FAST en la carga |
| Cobertura | **Sin cobertura**: la comprobación de solape es diseño pendiente |

**Enunciado.** Dos Safe Zones no comparten ningún tile.

**Razón.** Es el análogo exacto de [INV-TERR-002](#inv-terr-002), y falla por la misma razón: `SafeZoneIndex.ZoneAt(x, y)` es una función, y una función no puede devolver dos zonas para el mismo tile.

Aquí la ambigüedad tiene una consecuencia adicional que el territorio no tiene. Las safe zones tienen `zone_type` (`DENSE_FOREST` o `CAVERN`), y en cuanto los tipos difieran en algo —duración del ocultamiento, condiciones de entrada, efecto sobre el movimiento—, un tile perteneciente a dos zonas de tipos distintos tendría dos comportamientos. La unidad que se detenga ahí se ocultará según la zona que la implementación elija primero, y esa elección será invisible en el código y decisiva en el juego.

**Cómo se garantiza.**

- `DOMAIN` — la validación de carga comprobará el solape por pares antes de construir el índice, con la misma comparación de cuatro enteros que los territorios.
- `DOMAIN` — el índice `safeZoneOfTile` se construye escribiendo cada tile una sola vez; una segunda escritura sobre un tile ya asignado es la detección del solape.
- `DB` — **no hay garantía en la base de datos**: `safe_zones` sólo declara `safe_zones_type_valid` y `safe_zones_bounds_ordered`. Una exclusión de rangos exigiría `btree_gist` y una migración: **TBD (fuera de MVP)**.

**Cómo se verifica.**

- Test previsto: `Test_INV_SAFE_001_SafeZonesDoNotOverlap` (unit) — sobre el conjunto cargado, ningún par de rectángulos se solapa; zonas con aristas adyacentes se aceptan como control negativo.

**Violación en runtime.** Detección en la validación de carga. Log `invariant_violation` con `inv_id=INV-SAFE-001`, los dos `id` de zona y el tile en conflicto. Política `FAIL_FAST` del arranque.

---

<a id="inv-safe-002"></a>
## INV-SAFE-002 — Ningún tile de una Safe Zone es intransitable

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REPAIR (excluir el tile de la zona) |
| Cobertura | **Sin cobertura**: el índice de safe zones no existe todavía |

**Enunciado.** Ningún tile perteneciente a una Safe Zone es intransitable.

**Razón.** Una safe zone es un refugio, y un refugio al que no se puede entrar no es un refugio. Si el rectángulo de una zona incluyera tiles de `MOUNTAIN` o `WATER`, o tiles bloqueados por la zona urbana de una ciudad, esos tiles serían **zona segura inalcanzable**: contarían para la geometría, aparecerían en el índice y ninguna unidad podría llegar nunca a ellos.

El daño concreto no es que sobren tiles, sino que la zona **miente sobre su tamaño**. Un jugador que ve una zona de 20 × 20 asume 400 tiles de refugio; si la mitad es agua, tiene 200. Y como el ocultamiento se evalúa al detenerse ([INV-UNIT-008](units.md#inv-unit-008)), una unidad que intente refugiarse en el lado equivocado simplemente no se ocultará, sin ningún mensaje que explique por qué.

Nótese qué **no** exige el enunciado: no exige que la zona sea conexa, ni que todos sus tiles sean alcanzables entre sí. Exige que cada tile de la zona sea transitable, que es una propiedad local y barata de comprobar.

**Cómo se garantiza.**

- `DOMAIN` — la construcción del índice consultará `World.IsWalkable(x, y)` para cada tile del rectángulo y **excluirá** del índice los que no lo sean. El índice es, por tanto, la intersección del rectángulo con el terreno transitable, no el rectángulo entero.
- `DOMAIN` — excluir en lugar de rechazar la zona completa es deliberado: un rectángulo que roza una esquina de montaña sigue siendo una zona útil, y abortar el arranque por eso sería desproporcionado. Lo que no puede pasar es que el índice afirme que un tile de agua es refugio.
- `DOMAIN` — la comprobación usa `IsWalkable`, que combina terreno y capa de ocupación ([world.md](world.md)), de modo que una zona que se solapara con una zona urbana también quedaría recortada. Ver [INV-SAFE-006](#inv-safe-006), que trata ese caso concreto.
- `DB` — **no expresable como `CHECK`**: el terreno vive en `world_chunks` como `bytea` y la ocupación sólo existe en RAM.

**Cómo se verifica.**

- Test previsto: `Test_INV_SAFE_002_SafeZoneTilesAreWalkable` (unit) — para toda zona cargada, todo tile presente en el índice es transitable; un rectángulo que incluya agua produce un índice recortado, no un error.

**Violación en runtime.** Detección en la construcción del índice. Log de nivel `warn` con el número de tiles excluidos —es esperable en los bordes— y `invariant_violation` con `inv_id=INV-SAFE-002` si un tile intransitable **aparece** en el índice, que es lo que el invariante prohíbe. Política `REPAIR`: el tile se excluye.

---

<a id="inv-safe-003"></a>
## INV-SAFE-003 — `HIDDEN` implica estar en zona y en reposo

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REPAIR (volver a `IDLE`) |
| Cobertura | **Sin cobertura**: ninguna ruta produce `HIDDEN` hoy |

**Enunciado.** `units.status = 'HIDDEN'` implica que `SafeZoneIndex.ZoneAt(x, y)` devuelve una zona y que la unidad no tiene movimiento `ACTIVE`.

**Razón.** Es la formulación precisa de [INV-UNIT-008](units.md#inv-unit-008) contra el índice concreto, y su severidad es `CRITICO` porque una unidad oculta es una unidad **que otros jugadores no ven** ([INV-SAFE-004](#inv-safe-004)). Cualquier fallo de esta implicación es, literalmente, una ventaja de información obtenida sin cumplir la condición que la justifica.

Las dos mitades cierran dos vías distintas:

- **Ocultamiento fuera de zona** es invisibilidad en campo abierto. Un ejército que se moviera oculto rompería el interest management por completo: lo que está en tu área de interés y no está oculto, lo ves, y esa es la única garantía que el sistema ofrece al defensor.
- **Ocultamiento en movimiento** es más sutil y por eso está en el enunciado. Si una unidad pudiera conservar `HIDDEN` mientras se desplaza, bastaría con ocultarse dentro de la zona y salir andando: el ocultamiento se convertiría en un estado que se lleva puesto en lugar de una propiedad del lugar donde se está.

La conjunción de ambas es lo que hace el ocultamiento **local y verificable en todo instante**: basta mirar la posición y el estado del movimiento para saber si es legítimo.

**Cómo se garantiza.**

- `DOMAIN` — el paso a `HIDDEN` se evaluará únicamente cuando la unidad llegue a reposo, consultando `ZoneAt` para su tile. Nunca durante un movimiento.
- `DOMAIN` — aceptar una orden de movimiento pone `status = MOVING` en el mismo paso, y `units_status_valid` es un enum de un solo valor por fila: `HIDDEN` y `MOVING` no pueden coexistir por construcción.
- `DOMAIN` — la bicondicional `MOVING ⟺ movimiento ACTIVE` de [INV-UNIT-005](units.md#inv-unit-005) es lo que hace que «no `MOVING`» y «sin movimiento `ACTIVE`» sean la misma cosa; sin ese invariante, esta ficha tendría que comprobar las dos condiciones por separado.
- `DOMAIN` — la reconciliación de arranque reevaluará el ocultamiento de toda unidad `HIDDEN` contra el índice recién construido.

**Cómo se verifica.**

- Test previsto: `Test_INV_SAFE_003_HiddenImpliesInZoneAndAtRest` (integration) — barrido: ninguna unidad `HIDDEN` cae fuera del índice de zonas ni tiene movimiento `ACTIVE`.

**Violación en runtime.** Detección en la reconciliación de arranque y en una aserción previa a excluir una unidad de un delta. Log `invariant_violation` con `inv_id=INV-SAFE-003`, `unitId`, su tile y el estado de su movimiento. Política `REPAIR`: la unidad vuelve a `IDLE` y se emite `entity.spawn` a los suscriptores del chunk. Revelar una unidad que no cumplía la condición es preferible a ocultar una que no debía.

---

<a id="inv-safe-004"></a>
## INV-SAFE-004 — Una unidad oculta no aparece en deltas de terceros

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | BOUNDARY, TEST |
| Milestone | M7 |
| Política ante violación | FAIL_FAST del delta |
| Cobertura | **Sin cobertura**: `entity.despawn` ya declara `reason = HIDDEN` en el protocolo, pero nada lo emite todavía |

**Enunciado.** Ninguna unidad `HIDDEN` aparece en un delta dirigido a un jugador distinto de su propietario.

**Razón.** Es **el** invariante del ocultamiento: los demás definen cuándo una unidad puede estar oculta; éste es el que hace que ocultarse sirva de algo. Su violación no produce un estado incoherente sino una **fuga de información**, que es una clase de fallo peor por dos motivos: no deja rastro en ningún dato, y beneficia exactamente a quien no debería.

La formulación «dirigido a un jugador distinto de su propietario» es deliberada en las dos direcciones. El propietario **sí** ve sus propias unidades ocultas —ocultarse no es perderlas de vista—, y esa asimetría obliga a que el filtro se aplique **por destinatario**, no una sola vez al construir el delta. Es exactamente el punto donde este invariante se rompe en la práctica: se calcula un delta por chunk y se envía a todos sus suscriptores, con lo que el filtro correcto para el propietario se convierte en una fuga para el resto.

**Cómo se garantiza.**

- `BOUNDARY` — el filtrado se aplicará **por sesión de destino**, comparando `units.player_id` con el `playerId` de la sesión, no al construir el delta compartido. Es el único orden que respeta la asimetría.
- `BOUNDARY` — al pasar a `HIDDEN` se emitirá `entity.despawn` con `reason = HIDDEN` a los suscriptores del chunk que no sean el propietario; al dejar de estarlo, `entity.spawn`. El catálogo de razones de `entity.despawn` ya incluye `HIDDEN` (junto a `OUT_OF_INTEREST`, `DEAD`, `GARRISONED` y `REMOVED`): el contrato existe.
- `BOUNDARY` — `world.snapshot` aplicará el mismo filtro que los deltas. Un snapshot es un delta completo, y una unidad oculta que reapareciera al reconectar sería la misma fuga por otra puerta.
- `DOMAIN` — el ocultamiento no retira la unidad de la capa de ocupación: la unidad **sigue ocupando su tile**, a diferencia de una guarnecida ([INV-GARR-005](diplomacy.md#inv-garr-005)). Esa asimetría es intencional y merece decirse: una unidad oculta sigue estando físicamente ahí.

> **Nota honesta sobre el sondeo.** Que la unidad siga bloqueando su tile abre un canal de información indirecto: un jugador puede deducir la presencia de algo oculto viendo que su ruta lo rodea. Este invariante **no** cubre esa vía; cerrarla exigiría decidir si las unidades ocultas dejan de bloquear, lo que tiene consecuencias de gameplay que el MVP no aborda. Queda como **TBD (fuera de MVP)**, y se escribe aquí para que nadie asuma que el ocultamiento es hermético.

**Cómo se verifica.**

- Test previsto: `Test_INV_SAFE_004_HiddenInvisibleToThirdParties` (integration) — dos observadores suscritos al mismo chunk: el propietario sigue recibiendo `entity.update` de su unidad oculta; el tercero recibe un `entity.despawn` con `reason = HIDDEN` y ningún mensaje posterior sobre ella, tampoco en su siguiente `world.snapshot`.

**Violación en runtime.** Detección en una aserción del emisor de deltas que comprueba, por destinatario, que ninguna entidad `HIDDEN` ajena viaja en el mensaje. Log `invariant_violation` con `inv_id=INV-SAFE-004`, `unitId` y el `player_id` del destinatario. Política `FAIL_FAST` del delta: no se envía. Una fuga consumada se trata como incidente, no como bug ordinario: la información ya salió y no se puede retirar.

---

<a id="inv-safe-005"></a>
## INV-SAFE-005 — El rectángulo de una Safe Zone es válido y cabe en el mundo

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | FAIL_FAST en la carga |
| Cobertura | **Parcial**: la mitad `DB` está vigente (`safe_zones_bounds_ordered` aplicada); **no existe todavía** ningún test |

**Enunciado.** El rectángulo de una safe zone cumple `min_x <= max_x` y `min_y <= max_y` (`CHECK safe_zones_bounds_ordered`) y está contenido en `[0, EO_WORLD_WIDTH) × [0, EO_WORLD_HEIGHT)`.

**Razón.** Es el análogo de [INV-TERR-001](#inv-terr-001), con un matiz propio. Un territorio vacío se nota: nadie lo captura y alguien pregunta. Una **safe zone vacía no se nota nunca**: las unidades que intentan refugiarse en ella simplemente no se ocultan, y ese resultado es indistinguible de «la mecánica todavía no está» o «me detuve un tile fuera». El fallo se disfraza de comportamiento normal, que es la peor propiedad que puede tener un fallo.

Y como el ocultamiento es una ventaja de información, un rectángulo mal formado no produce una ventaja indebida sino una **desventaja silenciosa** para quien confía en la zona.

**Cómo se garantiza.**

- `DB` — `CONSTRAINT safe_zones_bounds_ordered CHECK (min_x <= max_x AND min_y <= max_y)` en `000001_initial_schema.up.sql`. Garantía **vigente hoy**.
- `DB` — `CONSTRAINT safe_zones_type_valid CHECK (zone_type IN ('DENSE_FOREST', 'CAVERN'))` cierra además el dominio del tipo, en línea con la convención de `text` + `CHECK` del canon §11.
- `DOMAIN` — la contención en el mundo se validará al cargar, contra las dimensiones del mundo ya construido. No es expresable como `CHECK` por la misma razón que en [INV-WORLD-001](world.md#inv-world-001): las dimensiones son configuración.
- `DOMAIN` — la validación precede a la construcción del índice, de modo que éste nunca se construye sobre geometría que no cabe.

**Cómo se verifica.**

- Test previsto: `Test_INV_SAFE_005_SafeZoneBoundsValid` (integration) — un `INSERT` con `min_x > max_x` falla por `safe_zones_bounds_ordered`; un `zone_type` desconocido falla por `safe_zones_type_valid`; una zona que se sale del mundo es rechazada por la carga.

**Violación en runtime.** Detección en el error de constraint y en la validación de carga. Log `invariant_violation` con `inv_id=INV-SAFE-005`, el `id` de la zona y el rectángulo. Política `FAIL_FAST` de la carga de esa zona: no se incorpora al índice, y ninguna unidad se ocultará en ella. Es preferible una zona ausente y visible en los logs a una zona presente que no protege.

---

<a id="inv-safe-006"></a>
## INV-SAFE-006 — Ninguna Safe Zone se solapa con una zona urbana

| Campo | Valor |
|---|---|
| Severidad | MEDIO |
| Aplicación | TEST |
| Milestone | M7 |
| Política ante violación | Log `error`; el tile queda fuera de la zona |
| Cobertura | **Sin cobertura**: ni el índice de zonas ni la comprobación existen todavía |

**Enunciado.** Ninguna Safe Zone se solapa con la zona urbana de una ciudad.

**Razón.** Es una propiedad de **coherencia de diseño** antes que de corrección: refugio y ciudad son dos mecánicas de protección distintas y no deben pisarse. Una safe zone sobre una zona urbana crearía un tile que está a la vez amurallado y oculto, y las dos mecánicas responden a preguntas incompatibles: la muralla dice «no se puede pasar por aquí», el refugio dice «aquí no se te ve». Combinadas, producen un tile intransitable donde el ocultamiento no puede llegar a evaluarse nunca, porque ninguna unidad puede detenerse ahí.

Por eso la severidad es `MEDIO` y no más: [INV-SAFE-002](#inv-safe-002) ya recorta del índice todo tile no transitable, y la zona urbana está bloqueada en la capa de ocupación, así que el solape **se resuelve solo** —los tiles urbanos simplemente no entran en el índice de la zona—. Lo que este invariante añade es la detección: un solape indica que alguien colocó una zona sobre una ciudad, y eso es un error de datos que hay que ver aunque sus consecuencias sean inocuas.

La separación mínima de 24 tiles entre centros de ciudad ([INV-CITY-008](city.md#inv-city-008)) deja sitio de sobra para colocar zonas sin tocar ninguna zona urbana; el solape no es una restricción incómoda, es una señal de descuido.

**Cómo se garantiza.**

- `TEST` — un barrido comprueba que ningún rectángulo de `safe_zones` interseca el 3 × 3 de ninguna ciudad. Es la única garantía, y así se declara: un invariante cuyo único nivel es `TEST` es frágil por definición ([README.md](README.md) §4).
- `DOMAIN` — el efecto práctico está cubierto por [INV-SAFE-002](#inv-safe-002): los tiles urbanos son intransitables y quedan fuera del índice. La zona sigue funcionando en su parte no solapada.
- `DB` — no expresable: la zona urbana no es una fila de ninguna tabla, sino un rectángulo derivado del centro de la ciudad y `townCenterRadius = 1`.

**Cómo se verifica.**

- Test previsto: `Test_INV_SAFE_006_SafeZonesDoNotOverlapCities` (integration) — para cada safe zone y cada ciudad, los rectángulos no se intersecan.

**Violación en runtime.** Detección en el test y en un aviso durante la construcción del índice. Log `invariant_violation` con `inv_id=INV-SAFE-006`, el `id` de la zona y el `city_id`. Sin reparación: el tile urbano queda fuera de la zona por la vía normal y la zona sigue operando en el resto de su superficie.

---

<a id="inv-safe-007"></a>
## INV-SAFE-007 — El índice tile → safe zone es determinista

| Campo | Valor |
|---|---|
| Severidad | MEDIO |
| Aplicación | TEST |
| Milestone | M7 |
| Política ante violación | Log `error` + métrica |
| Cobertura | **Sin cobertura**: el índice no existe todavía |

**Enunciado.** El índice `safeZoneOfTile` es función pura de `(safe_zones, terreno)`: reconstruirlo produce el mismo resultado byte a byte.

**Razón.** Es [INV-TERR-008](#inv-terr-008) con una entrada más, y la diferencia importa. El índice de territorios depende sólo de `territories`; el de safe zones depende **también del terreno**, porque [INV-SAFE-002](#inv-safe-002) recorta del índice los tiles intransitables. Eso lo encadena al determinismo del generador de mundo ([INV-WORLD-005](world.md#inv-world-005)): si el terreno regenerado difiere, el índice difiere, aunque las filas de `safe_zones` sean idénticas.

La consecuencia práctica es que este invariante es el **detector aguas abajo** de una deriva del generador. Un índice de safe zones que cambia entre dos arranques con la misma semilla y las mismas zonas significa que el terreno cambió, y eso es un problema mucho mayor que el índice.

Enumerar las dos entradas en el enunciado no es pedantería: dice exactamente qué hay que fijar para que el resultado sea reproducible, y evita que alguien busque el no-determinismo en el sitio equivocado.

**Cómo se garantiza.**

- `DOMAIN` — la construcción recorrerá las zonas en orden explícito de `id` y cada rectángulo en doble bucle `y` externo, `x` interno. Nunca una iteración de `map` ni goroutines escribiendo el mismo buffer.
- `DOMAIN` — la consulta de carga llevará `ORDER BY id` explícito.
- `DOMAIN` — el terreno consultado es el del mundo ya construido y validado, que es determinista por [INV-WORLD-005](world.md#inv-world-005). El índice se construye **después** de esa validación, nunca en paralelo.
- `DOMAIN` — el índice es un array plano indexado por `y*width + x`, la misma disposición fila-mayor que el terreno.

**Cómo se verifica.**

- Test previsto: `Test_INV_SAFE_007_SafeZoneIndexIsDeterministic` (unit) — construir el índice dos veces desde el mismo mundo y el mismo conjunto de zonas y comparar con `bytes.Equal`; barajar el orden de entrada y comprobar que el resultado no cambia.

**Violación en runtime.** Detección en el test de reconstrucción. Log `invariant_violation` con `inv_id=INV-SAFE-007`. Sin reparación: se conserva el índice existente y se investiga si la causa está en el índice o, más probablemente, en el terreno.

---

## Trazabilidad

| Invariante | Componente propietario | Relacionado con |
|---|---|---|
| INV-TERR-001 | `migrations/`, `internal/game/world` | [INV-WORLD-001](world.md#inv-world-001) |
| INV-TERR-002 | `internal/game/world` | [INV-WORLD-006](world.md#inv-world-006) |
| INV-TERR-003 | `migrations/` | [../database/schema.md](../database/schema.md) |
| INV-TERR-004 | `migrations/` | [../database/schema.md](../database/schema.md) |
| INV-TERR-005 | `internal/persistence/postgres` | [INV-PLAYER-005](player.md#inv-player-005) |
| INV-TERR-006 | `migrations/` | [../specs/territory.md](../specs/territory.md) |
| INV-TERR-007 | `internal/game/simulation` | [INV-CITY-011](city.md#inv-city-011) |
| INV-TERR-008 | `internal/game/world` | [INV-WORLD-005](world.md#inv-world-005) |
| INV-TERR-009 | `internal/websocket`, `internal/game/simulation` | [INV-WORLD-006](world.md#inv-world-006) |
| INV-SAFE-001 | `internal/game/world` | [INV-TERR-002](#inv-terr-002) |
| INV-SAFE-002 | `internal/game/world` | [INV-WORLD-003](world.md#inv-world-003) |
| INV-SAFE-003 | `internal/domain/unit`, `internal/game/simulation` | [INV-UNIT-008](units.md#inv-unit-008), [INV-UNIT-005](units.md#inv-unit-005) |
| INV-SAFE-004 | `internal/websocket`, `internal/game/simulation` | [INV-GARR-006](diplomacy.md#inv-garr-006), [INV-SEC-001](security.md#inv-sec-001) |
| INV-SAFE-005 | `migrations/`, `internal/game/world` | [INV-TERR-001](#inv-terr-001) |
| INV-SAFE-006 | `internal/game/world`, `internal/game/founding` | [INV-CITY-008](city.md#inv-city-008) |
| INV-SAFE-007 | `internal/game/world` | [INV-WORLD-005](world.md#inv-world-005), [INV-TERR-008](#inv-terr-008) |

## Documentos relacionados

- [README.md](README.md) — catálogo completo y tabla resumen.
- [world.md](world.md) — geometría del mundo, chunks y determinismo del terreno.
- [units.md](units.md) — estados de unidad, incluidos `HIDDEN` y `GARRISONED`.
- [diplomacy.md](diplomacy.md) — familia `INV-GARR-*`, la otra mecánica que oculta unidades.
- [../specs/territory.md](../specs/territory.md) y [../specs/safe-zones.md](../specs/safe-zones.md) — specs funcionales que citan estos IDs.
- [../database/schema.md](../database/schema.md) — DDL de `territories`, `territory_control` y `safe_zones`.

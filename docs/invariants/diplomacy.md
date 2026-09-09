# Invariantes de Diplomacia y Guarnición (INV-GARR-xxx)

Propiedades de la guarnición de unidades en ciudades: unicidad, coherencia con `units`, autorización por tratado, ocupación y visibilidad.

Formato y severidades: [README.md](README.md). Estados de unidad: [units.md](units.md). Ciudades: [city.md](city.md).

---

## Contexto: entidades creadas, mecánica diferida

`treaties` y `garrisons` existen en la migración `000001_initial_schema.up.sql` desde el primer día, pero **ninguna ruta de código Go escribe en ellas todavía**. Lo que sí está vivo hoy es el **estado** `GARRISONED` en `units.status` y su efecto sobre las órdenes: `unit.EnsureCanMove()` devuelve `unit.ErrGarrisoned` y la capa de aplicación lo traduce a `UNIT_GARRISONED`, uno de los 22 códigos de error del catálogo cerrado. Esa parte está implementada y testeada.

| Nivel | Estado real hoy |
|---|---|
| `DB` | **Vigente.** `garrisons.unit_id` es `PRIMARY KEY`, sus dos claves foráneas y `garrisons_city_idx` están aplicadas; `treaties` tiene sus cuatro constraints y su índice parcial único. |
| `DOMAIN` | **Parcial.** El estado `GARRISONED` rechaza órdenes de movimiento; entrar y salir de la guarnición no está implementado. |
| `BOUNDARY` | **Parcial.** `entity.despawn` declara `reason = GARRISONED` en el protocolo; nada lo emite todavía. |
| `TEST` | **Parcial.** El rechazo de órdenes está cubierto; el resto no. |

Cada ficha declara en su fila `Cobertura` qué parte está viva. Un invariante cuya única garantía vigente es `DB` se declara así.

### La tabla `garrisons` y el par canónico de `treaties`

```sql
CREATE TABLE garrisons (
    unit_id    bigint      PRIMARY KEY REFERENCES units (id) ON DELETE CASCADE,
    city_id    bigint      NOT NULL REFERENCES cities (id) ON DELETE CASCADE,
    entered_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX garrisons_city_idx ON garrisons (city_id);
```

La forma de la tabla **es** el invariante principal: al ser `unit_id` la clave primaria, «una unidad tiene como máximo una guarnición abierta» no es una regla que haya que comprobar, es una imposibilidad física ([INV-GARR-001](#inv-garr-001)).

`treaties` complementa el cuadro con el **par canónico**: `CHECK (player_a_id < player_b_id)` obliga a un orden fijo entre los dos firmantes, de modo que un tratado A–B y otro B–A no puedan coexistir como filas distintas; `CHECK (player_a_id <> player_b_id)` impide firmar consigo mismo; y `treaties_one_active_per_pair_and_type` —índice único parcial `WHERE status = 'ACTIVE'`— permite como máximo un tratado vigente de cada tipo entre dos jugadores. `allows_garrison boolean NOT NULL DEFAULT false` es el permiso concreto del que depende [INV-GARR-004](#inv-garr-004).

### Por qué guarnición y ocultamiento se documentan por separado

`GARRISONED` y `HIDDEN` ([territory.md](territory.md), familia `INV-SAFE-*`) coinciden en que ambos retiran la unidad de la vista de terceros, y por eso conviene fijar en qué se diferencian:

| | `GARRISONED` | `HIDDEN` |
|---|---|---|
| Ocupa tile del mundo | **No** ([INV-GARR-005](#inv-garr-005)) | Sí |
| Acepta órdenes | No (`UNIT_GARRISONED`) | Sí |
| Condición | Fila en `garrisons` | Estar en una Safe Zone, en reposo |
| Requiere autorización | Sí, si la ciudad es ajena | No |

La diferencia con más consecuencias es la primera: una unidad guarnecida **desaparece del mundo físico**, mientras que una oculta sigue bloqueando su tile.

---

<a id="inv-garr-001"></a>
## INV-GARR-001 — Una unidad tiene como máximo una guarnición abierta

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, TEST |
| Milestone | M7 |
| Política ante violación | FAIL_FAST (constraint) |
| Cobertura | **Parcial**: la mitad `DB` está vigente (`garrisons.unit_id` es `PRIMARY KEY`); **no existe todavía** ningún test |

**Enunciado.** Una unidad tiene como máximo una guarnición abierta: `garrisons.unit_id` es `PRIMARY KEY`.

**Razón.** Es el mismo problema que [INV-MOVE-001](movement.md#inv-move-001) resuelve para los movimientos, y se resuelve igual: haciendo imposible el estado inválido en el esquema en lugar de comprobarlo en código.

Dos guarniciones abiertas para la misma unidad significan que la unidad está en dos ciudades a la vez, y a partir de ahí todo lo que dependa de dónde está tiene dos respuestas: de qué ciudad forma parte la defensa, a qué `units.city_id` debe apuntar ([INV-GARR-003](#inv-garr-003)), a dónde vuelve al salir. El sistema tendría que elegir, y cualquier criterio es arbitrario.

Hay además una vía de abuso directa, análoga a la de los movimientos duplicados: si dos órdenes concurrentes crearan dos guarniciones, una sola unidad reforzaría dos ciudades simultáneamente. Es duplicación de un activo militar por una condición de carrera.

Que la clave sea `unit_id` y no un `id` sintético con un `UNIQUE` encima es una decisión de diseño con contenido: dice que **la identidad de una guarnición es la unidad guarnecida**. No hay historial de guarniciones en esta tabla; una fila existe mientras la unidad esté dentro y desaparece al salir. El historial, si se quisiera, iría a `world_events`.

**Cómo se garantiza.**

- `DB` — `garrisons.unit_id bigint PRIMARY KEY REFERENCES units (id) ON DELETE CASCADE`. La clave primaria hace estructuralmente imposible la segunda fila. Es la garantía **vigente hoy**.
- `DB` — `ON DELETE CASCADE` hacia `units`: una unidad borrada no deja guarniciones huérfanas.
- `DB` — `city_id bigint NOT NULL REFERENCES cities (id) ON DELETE CASCADE`: si la ciudad desaparece, la guarnición desaparece con ella. No hay guarniciones en ciudades inexistentes.
- `DOMAIN` — la entrada en guarnición comprobará la ausencia de fila previa antes de insertar, para devolver un error de dominio legible en lugar de un error de clave duplicada; pero la garantía dura es la de arriba.

**Cómo se verifica.**

- Test previsto: `Test_INV_GARR_001_OneOpenGarrisonPerUnit` (integration) — un segundo `INSERT` con el mismo `unit_id` falla por clave primaria; borrar la unidad borra su guarnición; borrar la ciudad también.

**Violación en runtime.** Imposible en escritura. En lectura, si el cargador encontrara dos guarniciones para una unidad —sólo posible tras una migración mal hecha—, log `invariant_violation` con `inv_id=INV-GARR-001`, `unitId` y ambos `city_id`. Política `FAIL_FAST` de la carga de esa unidad: no se incorpora a la simulación.

---

<a id="inv-garr-002"></a>
## INV-GARR-002 — `GARRISONED` si y sólo si existe fila de guarnición

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REPAIR (derivar desde `garrisons`) |
| Cobertura | **Parcial**: `TestRechazosDeMovimiento` / caso «unidad guarnecida» (`internal/game/simulation`) cubre el efecto del estado; la bicondicional con la tabla **no tiene test todavía** |

**Enunciado.** `units.status = 'GARRISONED'` si y sólo si existe una fila de `garrisons` para esa unidad.

**Razón.** Es exactamente el mismo patrón que [INV-UNIT-005](units.md#inv-unit-005) —un `status` desnormalizado que debe coincidir con la existencia de una fila en otra tabla— y sus dos modos de fallo son igual de asimétricos:

- **`GARRISONED` sin fila de guarnición** deja una unidad que no ocupa tile, no acepta órdenes y no está en ninguna ciudad. Es una unidad **perdida**: no hay operación que pueda sacarla, porque salir de la guarnición requiere saber de cuál se sale. El jugador ve desaparecer una unidad sin explicación y sin recurso.
- **Fila de guarnición con `status` distinto** es peor: la unidad figura como guarnecida en la ciudad y, a la vez, como presente en el mundo. Ocupa tile, aparece en los deltas de todos, acepta órdenes y puede alejarse caminando mientras sigue contando como defensora de la ciudad. Es duplicación efectiva de un activo.

Como en [INV-UNIT-005](units.md#inv-unit-005), la jerarquía de fuentes es la decisión clave y va en la misma dirección: **`garrisons` es la fuente de verdad**, `units.status` es la vista. La tabla es el registro transaccional del hecho; el `status` se toca también en otras rutas y tiene más superficie de error. Toda reparación deriva el `status` desde la tabla y nunca al revés.

**Cómo se garantiza.**

- `DOMAIN` — la entrada escribirá la fila de `garrisons` y pondrá `status = GARRISONED` en la **misma transacción**; la salida borrará la fila y devolverá la unidad a `IDLE` en la misma transacción. No habrá ninguna ruta que toque una sin la otra.
- `DOMAIN` — el `status` que rechaza órdenes es el que ya está implementado: `unit.EnsureCanMove()` devuelve `unit.ErrGarrisoned`, que la capa de aplicación traduce a `UNIT_GARRISONED`. Esa mitad es real hoy.
- `DOMAIN` — la reconciliación de arranque derivará el `status` de cada unidad desde `garrisons`, igual que ya se hace con `unit_movements`.
- `DB` — `units_status_valid` cierra el dominio de `status`, de modo que `GARRISONED` no puede confundirse con una variante que se colara por otra rama.

**Cómo se verifica.**

- `TestRechazosDeMovimiento` / caso «unidad guarnecida» (simulation, `internal/game/simulation`) — **existe y pasa**: una unidad `GARRISONED` rechaza `unit.move` con `UNIT_GARRISONED` y no se crea ningún movimiento.
- Test previsto: `Test_INV_GARR_002_GarrisonedIffRowExists` (integration) — barrido en las dos direcciones: ninguna unidad `GARRISONED` carece de fila, y ninguna fila corresponde a una unidad con otro `status`.

**Violación en runtime.** Detección en la reconciliación de arranque y en una aserción previa a emitir un delta con una unidad guarnecida. Log `invariant_violation` con `inv_id=INV-GARR-002`, `unitId` y el `status` observado. Política `REPAIR` derivando desde `garrisons`: si hay fila, `status = GARRISONED`; si no la hay, la unidad vuelve a `IDLE` sobre su última posición consolidada, que sigue siendo un tile válido. Se registra un `world_events` de auditoría.

---

<a id="inv-garr-003"></a>
## INV-GARR-003 — `units.city_id` coincide con la ciudad de la guarnición

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REPAIR (copiar desde `garrisons`) |
| Cobertura | **Sin cobertura**: ninguna ruta escribe `garrisons` todavía |

**Enunciado.** Si existe fila de `garrisons` para una unidad, `units.city_id` es igual a `garrisons.city_id`.

**Razón.** Es la tercera pieza de la misma desnormalización que cubren [INV-GARR-001](#inv-garr-001) y [INV-GARR-002](#inv-garr-002): la primera fija cuántas guarniciones, la segunda si la unidad está guarnecida, y ésta **en qué ciudad**. Sin ella, las dos anteriores pueden cumplirse y la unidad seguir estando en dos sitios a la vez, uno según cada tabla.

`units.city_id` existe porque es la columna que hace baratas las consultas por ciudad: `units_city_idx` es un índice parcial sobre ella, y contar la guarnición de una ciudad o listar sus unidades no debería exigir un `JOIN` con `garrisons` en el camino caliente. Ése es el beneficio; el riesgo de divergencia es su precio.

Dos consecuencias concretas de la divergencia: la ciudad A cree tener una defensora que en realidad está en B, y la unidad, al salir, reaparecería junto a una ciudad distinta de aquella en la que entró —un teletransporte gratuito entre ciudades, explotable en cuanto la mecánica exista—.

Nótese qué **no** dice el enunciado: no exige que `units.city_id` sea nulo cuando no hay guarnición. `city_id` tiene también el significado de «ciudad de origen» para unidades que nunca han entrado en ninguna guarnición, y es lo que sostiene el recuento de población de [INV-CITY-009](city.md#inv-city-009). La implicación es en un solo sentido, a propósito.

**Cómo se garantiza.**

- `DOMAIN` — la entrada escribirá `garrisons` y `units.city_id` en la misma transacción, con el mismo valor. No habrá ninguna ruta que actualice una sin la otra.
- `DOMAIN` — la reconciliación de arranque copiará `garrisons.city_id` a `units.city_id` para toda unidad con fila de guarnición: la tabla es la fuente ([INV-GARR-002](#inv-garr-002)).
- `DB` — ambas columnas referencian `cities (id)`, así que las dos apuntan siempre a ciudades existentes; lo que la base de datos no puede garantizar es que apunten **a la misma**.
- `DB` — la asimetría del `ON DELETE` merece atención: `garrisons.city_id` es `ON DELETE CASCADE` (la guarnición desaparece con la ciudad) y `units.city_id` es `ON DELETE SET NULL` (la unidad sobrevive sin ciudad). Borrar una ciudad deja, por tanto, un estado que cumple el enunciado de forma trivial —no hay fila de guarnición—, pero la unidad queda `GARRISONED` sin fila, lo que viola [INV-GARR-002](#inv-garr-002) y activa su reparación. Es el camino previsto y hay que conocerlo.

**Cómo se verifica.**

- Test previsto: `Test_INV_GARR_003_UnitCityMatchesGarrisonCity` (integration) — barrido: para toda fila de `garrisons`, `units.city_id` coincide; borrar una ciudad con guarniciones deja las unidades reparables por [INV-GARR-002](#inv-garr-002).

**Violación en runtime.** Detección en la reconciliación de arranque. Log `invariant_violation` con `inv_id=INV-GARR-003`, `unitId` y ambos `city_id`. Política `REPAIR`: prevalece `garrisons.city_id` y se reescribe `units.city_id`. La reparación es segura porque el valor correcto está en la tabla que es fuente.

---

<a id="inv-garr-004"></a>
## INV-GARR-004 — Guarnecer en ciudad ajena exige tratado activo

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REJECT con `TREATY_REQUIRED` |
| Cobertura | **Parcial**: `TREATY_REQUIRED` existe en el catálogo cerrado de 22 códigos, verificado por `TestCatalogoDeErroresCoincideConElExportado` (`internal/protocol`); la regla **no está implementada** |

**Enunciado.** Guarnición en ciudad ajena (`units.player_id <> cities.owner_player_id`) implica que existía un `treaty` `ACTIVE` con `allows_garrison = true` entre ambos jugadores en el instante de la entrada.

**Razón.** Es la regla que impide meter tropas dentro de la ciudad de otro sin su permiso, y su violación es la más grave de esta familia: una unidad dentro de una ciudad ajena es invisible para su dueño ([INV-GARR-006](#inv-garr-006)) y no ocupa tile ([INV-GARR-005](#inv-garr-005)). Sin autorización, eso es un caballo de Troya perfecto: tropas indetectables dentro de una fortificación que su propietario cree segura.

La cualificación **«en el instante de la entrada»** es la parte con más contenido y la más fácil de escribir mal. El tratado es un permiso **de entrada**, no una condición continua: si se comprobara en todo momento, romper un tratado expulsaría instantáneamente las guarniciones aliadas, lo que convertiría la ruptura en un arma que se dispara sola. Con la formulación correcta, romper un tratado impide **nuevas** entradas y deja que las existentes se resuelvan por las reglas de la diplomacia, que es donde esa decisión pertenece.

La contrapartida honesta es que el invariante, así formulado, no es verificable con un barrido del estado actual: exige el historial. Por eso su verificación se apoya en `world_events` —donde queda registrada la entrada con su tratado— y en `garrisons.entered_at`, que fecha la entrada y permite reconstruir qué tratados estaban vigentes entonces.

**Cómo se garantiza.**

- `DOMAIN` — la entrada en ciudad ajena consultará `treaties` buscando una fila con el par canónico de ambos jugadores, `status = 'ACTIVE'` y `allows_garrison = true`. Sin ella, se rechaza con `TREATY_REQUIRED` y no se escribe nada.
- `DOMAIN` — la comprobación va **antes** de cualquier escritura, como toda validación de autorización ([INV-SEC-004](security.md#inv-sec-004)); y el `playerId` con el que se compara viene de la sesión, no del payload ([INV-PLAYER-004](player.md#inv-player-004)).
- `DB` — el **par canónico** hace la consulta inequívoca: `CHECK (player_a_id < player_b_id)` obliga a ordenar los dos firmantes, de modo que un tratado A–B y otro B–A no pueden coexistir y la búsqueda no tiene que probar las dos combinaciones. `CHECK (player_a_id <> player_b_id)` impide el tratado consigo mismo.
- `DB` — `treaties_one_active_per_pair_and_type` (índice único parcial `WHERE status = 'ACTIVE'`) garantiza que la consulta devuelve como máximo una fila por tipo: no hay que decidir entre dos tratados vigentes contradictorios.
- `DB` — `treaties_status_valid` cierra el dominio a `PROPOSED`, `ACTIVE`, `EXPIRED`, `BROKEN`; `allows_garrison boolean NOT NULL DEFAULT false` hace que el permiso sea **opt-in**: un tratado que no lo menciona no lo concede.
- `DOMAIN` — la entrada registrará un `world_events` con el `treaty_id` que la autorizó, que es lo que hace auditable la cualificación temporal del enunciado.

**Cómo se verifica.**

- `TestCatalogoDeErroresCoincideConElExportado` (contract, `internal/protocol`) — **existe y pasa**: `TREATY_REQUIRED` está en el catálogo cerrado y coincide con el exportado por `packages/protocol`.
- Test previsto: `Test_INV_GARR_004_ForeignCityRequiresActiveTreaty` (integration) — sin tratado, la entrada se rechaza con `TREATY_REQUIRED` y no se escribe fila; con tratado `ACTIVE` y `allows_garrison = true`, se acepta; con tratado `ACTIVE` pero `allows_garrison = false`, se rechaza; romper el tratado **después** de la entrada no expulsa la guarnición existente pero sí impide una nueva.

**Violación en runtime.** Detección en la validación previa a la entrada y en una auditoría que cruza `garrisons` con `world_events`. Log `invariant_violation` con `inv_id=INV-GARR-004`, `unitId`, `city_id` y los dos `player_id`. Política `REJECT` antes del efecto. Una guarnición no autorizada **consumada** se trata como incidente de seguridad y se revisa manualmente: expulsarla automáticamente sería un efecto de gameplay decidido por un mecanismo de reparación, que es justo lo que este catálogo prohíbe.

---

<a id="inv-garr-005"></a>
## INV-GARR-005 — Una unidad guarnecida no ocupa tile

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REPAIR (retirar de la capa de ocupación) |
| Cobertura | **Sin cobertura**: la entrada en guarnición no está implementada; el efecto sobre la ocupación **no tiene test todavía** |

**Enunciado.** Ninguna unidad `GARRISONED` figura en la capa de ocupación del mundo.

**Razón.** Es la formulación concreta de [INV-UNIT-003](units.md#inv-unit-003) contra la capa de ocupación, y su modo de fallo es de los más desconcertantes para un jugador: un tile aparentemente vacío por el que no se puede pasar. El pathfinder lo esquiva, la ruta se alarga y no hay nada en pantalla que lo explique, porque la unidad que lo bloquea no se está emitiendo a nadie ([INV-GARR-006](#inv-garr-006)).

Hay además una fuga de información indirecta: quien vea a sus unidades rodear sistemáticamente un tile vacío junto a una ciudad ajena puede deducir que hay algo guarnecido ahí. Es débil, pero es real, y desaparece por completo si la unidad no ocupa nada.

Conviene contrastarlo con `HIDDEN`, que hace lo contrario a propósito: una unidad oculta **sí** sigue ocupando su tile, porque sigue estando físicamente en el mundo ([INV-SAFE-004](territory.md#inv-safe-004)). Guarnecer saca a la unidad del mundo; ocultarse sólo la quita de la vista. Confundir ambas cosas produce exactamente uno de los dos bugs: unidades guarnecidas que bloquean, o unidades ocultas que se atraviesan.

**Cómo se garantiza.**

- `DOMAIN` — el índice de ocupación se construirá a partir de una única consulta que filtra por los estados presentes en el mundo (`IDLE`, `MOVING`, `HIDDEN`), de modo que `GARRISONED` y `DEAD` quedan excluidos **por construcción** y no por un filtro repetido en cada llamada. Ésa es la garantía estructural.
- `DOMAIN` — la transición a `GARRISONED` retirará la unidad del índice en la misma unidad de trabajo en que escribe la fila de `garrisons`; la salida la reinsertará.
- `DOMAIN` — la posición consolidada de la unidad se conserva como dato histórico, pero **ninguna** lógica espacial la lee mientras esté guarnecida. Por eso [INV-UNIT-001](units.md#inv-unit-001) excluye explícitamente este estado.
- `DOMAIN` — la capa de ocupación es un índice **derivado y reconstruible** desde `units` más el mundo; eso es lo que hace segura la política `REPAIR`.

**Cómo se verifica.**

- Test previsto: `Test_INV_GARR_005_GarrisonedNotInBlockedOverlay` (unit) — tras guarnecer, la capa de ocupación no contiene la unidad y un path que atraviesa su antiguo tile tiene éxito; al salir, el tile vuelve a estar ocupado.

**Violación en runtime.** Detección en la reconciliación periódica que compara la capa de ocupación con los estados de `units`. Log `invariant_violation` con `inv_id=INV-GARR-005`, `unitId` y el tile. Política `REPAIR`: se retira la unidad de la capa. La reparación es segura porque el estado autoritativo es `units.status` —derivado a su vez de `garrisons` ([INV-GARR-002](#inv-garr-002))— y la ocupación es un índice reconstruible.

---

<a id="inv-garr-006"></a>
## INV-GARR-006 — Una unidad guarnecida no aparece en deltas de terceros

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | BOUNDARY, TEST |
| Milestone | M7 |
| Política ante violación | FAIL_FAST del delta |
| Cobertura | **Parcial**: `entity.despawn` ya declara `reason = GARRISONED` en el protocolo v1; **nada lo emite todavía** |

**Enunciado.** Ninguna unidad `GARRISONED` aparece en un delta dirigido a un jugador distinto de su propietario.

**Razón.** Es la razón de ser de la guarnición como mecánica: meter tropas en una ciudad las pone a salvo **y las esconde**. Si la unidad siguiera emitiéndose, aparecería en pantalla dentro de una ciudad, revelando la presencia y el número de tropas guarnecidas, que es información con valor táctico directo: quien la tenga sabe si atacar o no.

La formulación «dirigido a un jugador distinto de su propietario» es idéntica a la de [INV-SAFE-004](territory.md#inv-safe-004) y por la misma razón: el propietario **sí** ve sus propias unidades guarnecidas —guarnecer no es perderlas—, y esa asimetría obliga a filtrar **por destinatario**. Es el punto exacto donde el invariante se rompe en la práctica: se construye un delta por chunk y se envía a todos sus suscriptores, con lo que el filtro correcto para el propietario se convierte en una fuga para el resto.

Hay un matiz que la guarnición añade y el ocultamiento no tiene: la unidad puede estar en una ciudad **ajena** (con tratado, [INV-GARR-004](#inv-garr-004)). En ese caso hay tres partes —el dueño de la unidad, el dueño de la ciudad y todos los demás— y el enunciado es deliberadamente estricto: sólo el dueño de la unidad la ve. Si el dueño de la ciudad debe ver o no las tropas aliadas que aloja es una decisión de diseño diplomático, no de este invariante, y es **TBD (fuera de MVP)**.

**Cómo se garantiza.**

- `BOUNDARY` — el filtrado se aplicará **por sesión de destino**, comparando `units.player_id` con el `playerId` de la sesión, nunca al construir el delta compartido.
- `BOUNDARY` — al entrar en guarnición se emitirá `entity.despawn` con `reason = GARRISONED` a los suscriptores del chunk que no sean el propietario; al salir, `entity.spawn`. El catálogo de razones de `entity.despawn` ya incluye `GARRISONED` (junto a `OUT_OF_INTEREST`, `DEAD`, `HIDDEN` y `REMOVED`): el contrato existe y no hay que ampliarlo.
- `BOUNDARY` — `world.snapshot` aplicará el mismo filtro que los deltas. Un snapshot es un delta completo, y una unidad guarnecida que reapareciera al reconectar sería la misma fuga por otra puerta.
- `DOMAIN` — al no figurar en la capa de ocupación ([INV-GARR-005](#inv-garr-005)), la unidad tampoco es deducible por el trazado de rutas ajenas. Los dos invariantes juntos cierran el canal directo y el indirecto, que es más de lo que consigue el ocultamiento.

**Cómo se verifica.**

- Test previsto: `Test_INV_GARR_006_GarrisonedInvisibleToThirdParties` (integration) — dos observadores suscritos al chunk de la ciudad: el propietario de la unidad sigue viéndola; el tercero recibe un `entity.despawn` con `reason = GARRISONED` y ningún mensaje posterior sobre ella, tampoco en su siguiente `world.snapshot`.

**Violación en runtime.** Detección en una aserción del emisor de deltas que comprueba, por destinatario, que ninguna entidad `GARRISONED` ajena viaja en el mensaje. Log `invariant_violation` con `inv_id=INV-GARR-006`, `unitId` y el `player_id` del destinatario. Política `FAIL_FAST` del delta: no se envía. Una fuga consumada se trata como incidente: la información ya salió y no se puede retirar.

---

<a id="inv-garr-007"></a>
## INV-GARR-007 — Entrar y salir de la guarnición no cambia el dueño

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | M7 |
| Política ante violación | REJECT (la escritura no se aplica) |
| Cobertura | **Sin cobertura**: ninguna ruta escribe `garrisons` todavía; `units.player_id` no lo modifica nada hoy ([INV-UNIT-010](units.md#inv-unit-010)) |

**Enunciado.** El ownership (`units.player_id`) de una unidad guarnecida no cambia al entrar ni al salir.

**Razón.** Puede parecer obvio hasta que se mira la implementación más natural. Guarnecer escribe `units.city_id`, y la ciudad tiene dueño; de ahí a «la unidad ahora pertenece a la ciudad, luego al dueño de la ciudad» hay un paso muy corto, y es exactamente el paso que este invariante prohíbe. En una guarnición en ciudad ajena, ese paso **regala la unidad** al aliado que la aloja.

El caso inverso es igual de dañino y menos evidente: si al salir el ownership se recalculara desde `city_id` —que puede haber quedado apuntando a la ciudad anfitriona—, la unidad volvería al mundo perteneciendo a otro jugador. Guarnecerse en casa de un aliado y salir se convertiría en una transferencia de propiedad silenciosa, en cualquiera de las dos direcciones según cómo esté escrito el cálculo.

El invariante conecta con dos fichas de [units.md](units.md): [INV-UNIT-006](units.md#inv-unit-006) («una unidad pertenece a un único jugador») y sobre todo [INV-UNIT-010](units.md#inv-unit-010) («`player_id` y `unit_type` son inmutables tras la creación»), del que éste es un caso particular. Se documenta aparte porque la guarnición es **la primera mecánica que introduce una razón plausible para tocar `player_id`**, y conviene que quede escrito que no debe tocarlo.

**Cómo se garantiza.**

- `DOMAIN` — la entrada y la salida escribirán exclusivamente `garrisons`, `units.city_id` y `units.status`. `player_id` no está en la lista de columnas de ninguna de las dos sentencias.
- `DOMAIN` — el ownership es inmutable en MVP ([INV-UNIT-010](units.md#inv-unit-010)): no existe ninguna ruta de transferencia, así que tampoco hay una función que la guarnición pudiera invocar por error.
- `DOMAIN` — la comprobación de autorización de [INV-GARR-004](#inv-garr-004) compara `units.player_id` con `cities.owner_player_id` precisamente porque son **distintos** en una guarnición aliada. Que sigan siendo distintos después es lo que mantiene esa comparación con significado: si guarnecer igualara ambos, la comprobación pasaría a ser trivialmente cierta en la salida.
- `DOMAIN` — el filtro de visibilidad de [INV-GARR-006](#inv-garr-006) usa `units.player_id` para decidir quién ve la unidad. Un ownership cambiado no sólo regalaría la unidad: se la mostraría al nuevo dueño y la ocultaría al antiguo.

**Cómo se verifica.**

- Test previsto: `Test_INV_GARR_007_OwnershipPreservedAcrossGarrison` (integration) — se toma la huella de `(id, player_id)` de la unidad, se guarnece en una ciudad ajena con tratado, se sale, y la huella no cambia.

**Violación en runtime.** Detección en una aserción posterior a la entrada y a la salida que compara `player_id` con el valor previo. Log `invariant_violation` con `inv_id=INV-GARR-007`, `unitId`, dueño anterior y nuevo. Política `REJECT`: la escritura no se aplica. Un cambio consumado de ownership se trata como incidente, igual que en [INV-UNIT-006](units.md#inv-unit-006): equivale a regalar un activo.

---

## Cobertura conjunta

Las siete fichas se refuerzan entre sí. Vistas como una sola propiedad: *una unidad guarnecida está en exactamente una ciudad, la misma según las dos tablas que lo dicen, con autorización si la ciudad es ajena, fuera del mundo físico, invisible para terceros y sin haber cambiado de dueño.*

```
      INV-GARR-001  a lo sumo una guarnición por unidad   (PRIMARY KEY)
             │
             ▼
      INV-GARR-002  GARRISONED  ⟺  existe fila
             │
             ├── INV-GARR-003  units.city_id == garrisons.city_id
             └── INV-GARR-004  ciudad ajena exige tratado ACTIVE
             │
             ▼
      INV-GARR-005  no figura en la capa de ocupación
             │
             ▼
      INV-GARR-006  no aparece en deltas de terceros
             │
             ▼
      INV-GARR-007  el ownership no cambia
```

<a id="inv-garr-008"></a>
## INV-GARR-008 — Coherencia entre gracia de expulsión y tratado vigente (RESERVADO)

**Estado: reservado, no vigente.** Este ID está apartado y **no debe reutilizarse**, pero el
invariante todavía no se puede enunciar de forma verificable.

**Enunciado previsto.** Toda unidad guarnecida en una ciudad ajena cuyo tratado ha dejado de estar
`ACTIVE` tiene una gracia de expulsión en curso y no vencida.

**Por qué no está vigente.** La gracia no tiene dónde vivir: la tabla `garrisons` no tiene columna
para su vencimiento, y la política de expulsión diferida al romperse un tratado está descrita en
[../specs/garrison.md](../specs/garrison.md) §6.4 como diseño objetivo, no como comportamiento
implementado. Un invariante que no se puede comprobar no es un invariante: es una intención.

**Cuándo pasa a estar vigente.** En la misma migración aditiva que introduzca el vencimiento de la
gracia. Esa PR debe, a la vez: añadir la columna, implementar la expulsión, escribir el test, y
sustituir esta ficha por una completa con su mecanismo de garantía y su política ante violación.

**Registrado aquí y no sólo en la spec** porque el registro de invariantes es el dueño único de la
numeración: un ID mencionado en una spec pero ausente del registro es exactamente la clase de deriva
que `scripts/check-docs.mjs` existe para impedir.

---

## Trazabilidad

| Invariante | Componente propietario | Relacionado con |
|---|---|---|
| INV-GARR-001 | `migrations/` | [INV-MOVE-001](movement.md#inv-move-001) |
| INV-GARR-002 | `internal/domain/unit`, `internal/game/simulation` | [INV-UNIT-005](units.md#inv-unit-005), [INV-UNIT-007](units.md#inv-unit-007) |
| INV-GARR-003 | `internal/persistence/postgres` | [INV-CITY-009](city.md#inv-city-009) |
| INV-GARR-004 | `internal/game/simulation` | [INV-SEC-004](security.md#inv-sec-004), [INV-CITY-006](city.md#inv-city-006) |
| INV-GARR-005 | `internal/game/world`, `internal/game/simulation` | [INV-UNIT-003](units.md#inv-unit-003) |
| INV-GARR-006 | `internal/websocket`, `internal/game/simulation` | [INV-SAFE-004](territory.md#inv-safe-004) |
| INV-GARR-007 | `internal/domain/unit` | [INV-UNIT-006](units.md#inv-unit-006), [INV-UNIT-010](units.md#inv-unit-010) |
| INV-GARR-008 | *reservado, sin propietario todavía* | [INV-GARR-004](#inv-garr-004) |

## Documentos relacionados

- [README.md](README.md) — catálogo completo y tabla resumen.
- [units.md](units.md) — estados de unidad; [INV-UNIT-003](units.md#inv-unit-003) y [INV-UNIT-007](units.md#inv-unit-007) son las contrapartes de esta familia.
- [territory.md](territory.md) — familias `INV-TERR-*` e `INV-SAFE-*`; `HIDDEN` es la otra mecánica que oculta unidades.
- [city.md](city.md) — presencia y protección de la ciudad anfitriona.
- [../specs/garrison.md](../specs/garrison.md) — spec funcional que cita estos IDs.
- [../database/schema.md](../database/schema.md) — DDL de `garrisons` y `treaties`.

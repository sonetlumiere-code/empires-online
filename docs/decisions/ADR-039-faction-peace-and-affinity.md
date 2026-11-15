# ADR-039: La facción es una segunda fuente de paz, se une con preaviso y su tierra se calcula en cada golpe

Propósito: fijar cómo el slice de facciones ([SPEC-FACTIONS](../specs/factions.md)) hace que el bando de un jugador
cambie a quién puede agredir y con quién puede firmar, sin una fila por pareja, sin que una entrada unilateral dé paz
en el acto, sin que la base quede nunca con un pacto entre la Orden y el Caos sin preaviso, y sin tocar el modelo de
movimiento.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-15** |
| Ámbito | `internal/domain/player` (los códigos y las reglas del bando), `internal/domain/city` (la facción y la entrada pendiente en la ciudad), `internal/game/simulation` (`faction.join`, el vencimiento en la fase 5, `AtPeace`, la validación de los tratados, el daño en tierra afín), `internal/persistence/postgres` (la carga, la orden y el vencimiento), `internal/game/loadcheck`, migración `000019`, `internal/observability`, `packages/protocol`, cliente |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-016](ADR-016-foreign-territory-actions.md) (qué es una acción hostil), [ADR-022](ADR-022-durable-write-retry-and-loop-inbox.md) (ninguna escritura se pierde en silencio), [ADR-027](ADR-027-hot-treaties.md) (los tratados en caliente, su preaviso y la paz evaluada por fecha), [ADR-028](ADR-028-event-log-from-world-events.md) (quién rompió, en el payload), [ADR-030](ADR-030-stone-layer-and-era-advance.md) (el encargo en dos fases y el fin optimista del avance de era), [ADR-032](ADR-032-trade-offers-and-two-key-jobs.md) (la escalera de tratados), [ADR-034](ADR-034-territory-claims.md) (el control del territorio en la RAM), [ADR-035](ADR-035-civilization-traits.md) (los enteros por mil en el punto de consumo, la civilización en la ciudad) |

---

## Contexto

La Global Faction existe desde M2 como estructura: el catálogo `factions` (`ORDER`, `CHAOS`, `NEUTRAL`, con `id`
fijos 1, 2 y 3), la columna `players.faction_id NOT NULL` y la regla de que es un eje ortogonal a la civilización
(`RN-PLAYER-005`). Todos los jugadores nacen `NEUTRAL` por un parámetro de arranque y ningún sistema la lee.

Piezas que ya existen y que el slice usa:

- **Un solo punto de paz.** `State.AtPeace(x, y, now)` dice si dos jugadores tienen un tratado vigente que impide
  agredir, también durante su preaviso; lo consultan el combate (`unit.attack`, también contra una caravana), el saqueo
  y el asedio (`unit.pillage`, `unit.demolish`) y la conquista (`unit.claim` y el avance de un estandarte), al dar la
  orden y en cada tick. La respuesta automática de E3 sólo nace de un golpe, que ya pasó por él.
- **Los tratados en la RAM.** Los abiertos viven en `diplomacyState`; una orden reserva el par (`busy`) hasta que su
  transacción vuelve por el buzón, y los vencimientos se aplican en la fase 5 de forma optimista con su compensación
  ([ADR-027](ADR-027-hot-treaties.md) §2 y §3). `TreatyRepo.ApplyTreaty` escribe una transición con su evento y es
  idempotente.
- **La civilización en la ciudad.** La RAM conoce la civilización de un jugador por su ciudad (`city.City.Civilization`),
  que la carga lee de `players` y la fundación trae con `IntroducePlayer` ([ADR-035](ADR-035-civilization-traits.md) §4).
- **El patrón del avance de era.** El encargo va en dos fases —el tick reserva, la cola escribe, el buzón confirma— y
  el fin, en la fase 5, es optimista con su reencolado ([ADR-030](ADR-030-stone-layer-and-era-advance.md) §4 y §5).
- **El control del territorio en la RAM**, con `TerritoryAt` y `Control.PlayerOwner`
  ([ADR-034](ADR-034-territory-claims.md)).

## Decisión

### 1. La facción vive en la ciudad, como la civilización

`city.City` gana `Faction` (el código efectivo) y `FactionJoin` (la entrada pendiente: el código y el instante en que
cuenta, o nada). No son columnas de `cities`: la carga las lee de `players` con la misma subconsulta que la
civilización, y `IntroducePlayer` trae `NEUTRAL` sin entrada. Un jugador sin ciudad, o una ciudad sin código (los tests
que no lo fijan), es `NEUTRAL`. La validación de carga exige que toda ciudad tenga una facción del catálogo y que una
entrada pendiente sólo exista sobre `NEUTRAL` y hacia `ORDER` o `CHAOS`.

### 2. La facción es una segunda fuente de paz

`AtPeace` pasa a ser «hay un tratado que da paz **o los dos son de `ORDER`**», con la facción efectiva, nunca con la
pendiente. Sigue siendo el único punto: las cuatro acciones hostiles, la captura de caravanas, el avance de un
estandarte y el fin de los enfrentamientos (`RN-COMBAT-008`, `RN-COMBAT-025`) quedan cubiertos sin tocar cada sistema.
Un predicado aparte (`orderPeace`) dice si la paz viene del bando, para que el rechazo diga «los dos sois de la Orden»
en lugar del motivo de un tratado. La paz de la Orden no da guarnición: `allows_garrison` sigue siendo de la alianza.

La validación de `treaty.propose` y de `treaty.respond` con `accept` rechaza un `NON_AGGRESSION` o una `ALLIANCE`
entre `ORDER` y `CHAOS` con `INVALID_TARGET` (`RN-FAC-005`), mirando la facción efectiva en el momento de la orden.

### 3. Unirse va en dos fases y cuenta tras un preaviso

`faction.join` es un comando de la simulación, no hostil. El tick valida (`NEUTRAL`, sin entrada pendiente ni en
vuelo), apunta la entrada en la RAM como **reservada** —invisible para todos, como un avance que espera su
transacción— y encola `faction.join` (clave `player:<id>`), que escribe `faction_join_id` y `faction_join_at` sólo si
el jugador sigue `NEUTRAL` sin entrada. Al volver por el buzón la entrada se confirma y su ciudad se difunde con
`factionJoin`; un rechazo la borra y responde el código. El instante en que cuenta lo fija el tick al dar la orden
(ahora + `EO_FACTION_JOIN_SECONDS`).

La entrada no se retira. Si la RAM y la base discrepan (un reintento tras un reinicio), manda la base: la escritura es
idempotente sobre el mismo instante.

### 4. El vencimiento es optimista y escribe la facción y las rupturas en una transacción

En la fase 5, cada entrada confirmada que venció se aplica en orden de ciudad, **salvo** que alguno de los pares del
jugador con un tratado que vaya a romper tenga una orden de tratado en vuelo (`busy`): entonces espera al tick
siguiente. Una orden en vuelo se resuelve en milisegundos, y esperar evita aplicar una ruptura sobre un tratado cuyo
estado está a punto de cambiar.

Al aplicarla, en el mismo tick:

1. La facción de la ciudad pasa a ser la elegida y la entrada se borra.
2. Todo `NON_AGGRESSION` o `ALLIANCE` `ACTIVE` sin preaviso con un jugador del bando contrario empieza el suyo
   (`BreakEffectiveAt` = ahora + `EO_TREATY_BREAK_NOTICE_MINUTES`), como si lo hubiera roto quien se une (`By`), y se
   difunde a sus dos firmantes.
3. Si se unió a `ORDER`, los enfrentamientos, saqueos, derribos y estandartes con otros de `ORDER` terminan en la fase
   siguiente por el mismo camino que cuando nace un tratado, porque `AtPeace` ya responde que sí.
4. La ciudad se difunde con `faction` y `factionJoin: null`, y cada territorio de su dueño, con su `ownerFaction`
   nuevo.
5. Un único trabajo, `faction.joined` (clave `player:<id>`), escribe en **una transacción** la facción, el borrado
   de la entrada, cada ruptura con su `TreatyBreakAnnounced` (con `byPlayerId` y `atMs`, ADR-028 §3) y
   `FactionJoined`.

La transacción es tolerante: la facción sólo se escribe si el jugador sigue `NEUTRAL` con esa entrada (un reintento
encuentra la facción ya escrita y termina), y cada ruptura sólo se escribe sobre una fila `ACTIVE` sin
`break_effective_at` (si otra escritura la cerró o la rompió, se omite sin error ni evento). Así el trabajo puede ir con
la clave del jugador y no con las claves de los pares —un trabajo de muchas claves bloquearía a varios workers
(ADR-032 §3)— sin que un orden distinto entre colas deje la base incoherente. Si falla del todo, la compensación lo
reencola en el barrido siguiente (ADR-022), como el fin de un avance.

Con esto la base nunca tiene un jugador de un bando con un pacto o alianza vigente sin preaviso con el bando
contrario: o la transacción entera está escrita, o la entrada sigue pendiente y vencida, y el primer tick tras un
reinicio la vuelve a aplicar.

### 5. La tierra afín se calcula en el golpe

En la fase 4, al aplicar cada golpe a una **unidad**, la simulación mira el tile de la víctima en ese instante, su
territorio, el dueño de su control y la facción efectiva de ese dueño. Es afín si el dueño es el de la víctima o si
los dos son del mismo bando y ese bando no es `NEUTRAL`; entonces el daño es `(daño × r + 500) / 1000`, nunca menos
de 1, con `r` = `EO_FACTION_AFFINE_DAMAGE_PERMILLE`. Nada se guarda: si el territorio cambia de manos o su dueño entra
en un bando, el golpe siguiente ya lo nota. Los golpes de saqueo y de asedio no se tocan. El daño reducido es el que
llega al `hp` de la vista y a los golden; el cliente no lo calcula.

### 6. La migración `000019`

`players` gana `faction_join_id integer NULL REFERENCES factions (id)` y `faction_join_at timestamptz NULL`, con el
`CHECK` `players_faction_join_pair` (las dos nulas o ninguna). El trigger `players_faction_transition`
(`BEFORE UPDATE OF faction_id, faction_join_id ON players`) rechaza con `23514` y el ID del invariante cualquier cambio
de `faction_id` que no sea de `NEUTRAL` a `ORDER` o `CHAOS`, y cualquier entrada pendiente sobre un jugador que no es
`NEUTRAL` o hacia `NEUTRAL`; compara por código del catálogo, no por `id`. No crea tablas.

## Alternativas descartadas

- **Un pacto implícito por cada pareja de la Orden.** Filas en `treaties` creadas al entrar: crecen con el cuadrado de
  los jugadores de la Orden, tendrían que nacer y romperse con la semántica de preaviso de un tratado que nadie firmó,
  y llenarían la diplomacia de cada jugador. La paz de la Orden es una regla, no un acuerdo.
- **La facción como sesión, fuera del tick.** `faction.join` atendido por la conexión, como los mensajes de E16: la
  facción cambia lo que la simulación permite, y la simulación es la única que puede decidir en qué tick empieza la
  paz y terminan los enfrentamientos.
- **Entrada inmediata.** Daría paz en el acto frente a un atacante de la Orden sin que él aceptara nada, lo que ningún
  tratado permite. El preaviso tiene la forma del de ruptura.
- **Rechazar la entrada con tratados incompatibles.** Obligaría a romperlos antes a mano y no resolvería el caso de dos
  firmantes que entran en bandos contrarios a la vez.
- **Rupturas por la vía de la orden de tratado**, una por par con su reserva y su clave: la base pasaría por un estado
  con la facción escrita y un pacto con el bando contrario sin preaviso, y un reinicio en medio lo dejaría así.
- **Un barrido que rompa en cualquier momento lo incompatible.** Una red de seguridad sin autor: no sabría a quién
  atribuir la ruptura en el registro, y con la transacción única no hay estado que reparar.
- **La afinidad guardada** en el control del territorio o en la unidad: habría que recalcularla en cada conquista, en
  cada entrada en un bando y en cada paso, para ahorrar una búsqueda en un mapa por golpe.
- **Un bono de velocidad en tierra afín.** La polilínea tiene una duración por paso fijada al dar la orden; un bono
  por tile obligaría a duraciones distintas por tile y cambiaría el modelo de movimiento y su recuperación.
- **Territorios cuyo dueño es una facción** (`owner_type = 'FACTION'`): cambia la conquista y la población de E12, y
  no hace falta para que la tierra del bando cuente.

## Consecuencias

- `AtPeace` deja de ser sólo diplomacia: quien lo lea debe saber que también responde por el bando. El test de la
  tabla de §4.1 de la spec lo fija celda a celda.
- Un jugador de la Orden no puede capturar caravanas de otro de la Orden: la captura es un `unit.attack`.
- La entrada puede esperar unos ticks más que su instante si hay una orden de tratado en vuelo con el bando
  contrario; el retraso es del orden del tiempo de una transacción.
- `eo_persistence_queue_depth` no cambia de forma: los dos trabajos nuevos son de una clave.
- El cliente copia del servidor el porcentaje de la tierra afín para el texto, como ya copia otros valores del
  catálogo (pendiente general de mandarlos en el protocolo).
- Si algún día se puede cambiar de bando o volver a `NEUTRAL`, este ADR se sustituye: el trigger, el preaviso y la
  regla «sólo desde `NEUTRAL`» son los puntos que cambiarían.

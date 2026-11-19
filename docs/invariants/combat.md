# Invariantes de Combate (INV-COMBAT-xxx)

Propiedades del combate: la vida de una unidad, la muerte, el botín, las reglas que impiden un golpe, desde E5 el
saqueo del almacén de una ciudad y, desde E27, la muralla y el ariete.

Formato y severidades: [README.md](README.md). Unidades: [units.md](units.md). Ciudades: [city.md](city.md).
Economía: [economy.md](economy.md). Specs: [../specs/combat-raid.md](../specs/combat-raid.md) y
[../specs/combat-pillage.md](../specs/combat-pillage.md). Decisiones:
[ADR-024](../decisions/ADR-024-combat-simultaneous-resolution.md) y
[ADR-026](../decisions/ADR-026-pillage-two-phase-stock-debit.md).

---

<a id="inv-combat-001"></a>
## INV-COMBAT-001 — Una unidad está `DEAD` si y sólo si su `hp` es 0

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E2 |
| Política ante violación | REJECT (escritura) · REPAIR (arranque) |
| Cobertura | **Cubierto**: la DB por `Test_INV_COMBAT_001_LaConstraintRechazaUnaMuertaConVida` y `TestLaMuerteSeEscribeEnUnaTransaccionIdempotente` (integration, `internal/persistence/postgres`); el dominio por `TestElGuerreroPersigueYMataAlAldeano` (`internal/game/simulation`) |

**Enunciado.** Para toda fila de `units`, `status = 'DEAD'` si y sólo si `hp = 0`; y lo mismo para toda unidad
en la RAM del Game Server.

**Razón.** Una unidad con `hp = 0` que no está `DEAD` sigue aceptando órdenes y ocupando población; una `DEAD`
con vida podría volver a jugar al rehidratarse. Las dos son resurrecciones o zombis que ningún jugador
entendería.

**Cómo se garantiza.**

- `DB` — `CHECK ((status = 'DEAD') = (hp = 0))` en `units` (migración `000007`).
- `DOMAIN` — el único camino que deja `hp = 0` es la muerte de la fase 4 (`RN-COMBAT-009`), que pone `DEAD`
  en la misma asignación; la transacción de muerte escribe las dos columnas a la vez.

**Cómo se verifica.** `Test_INV_COMBAT_001_LaConstraintRechazaUnaMuertaConVida` (integration) — **existe y pasa**: el
motor rechaza las dos mitades del bicondicional por su nombre. `TestLaMuerteSeEscribeEnUnaTransaccionIdempotente`
(integration) y `TestElGuerreroPersigueYMataAlAldeano` (unit) — **existen y pasan**.

**Violación en runtime.** La base rechaza la escritura; la cola la registra y la compensación vuelve al loop.

---

<a id="inv-combat-002"></a>
## INV-COMBAT-002 — Ningún golpe cae donde la protección, el refugio o el ocultamiento lo prohíben

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | E2 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestNingunGolpeCaeSobreUnaUnidadEnSafeZone`, `TestRechazosDelAtaque`, `TestLaProteccionAMitadDePersecucionTerminaElEnfrentamiento`, `TestUnIncursorProtegidoDejaDeGolpear`, `TestUnJugadorProtegidoNoPuedeAtacar` y, desde E15, `TestLaProteccionDelDuenoNoCubreSuCaravana` (`internal/game/simulation`) |

**Enunciado.** Ningún golpe se aplica si, en el instante del golpe, el atacante o el objetivo están sobre un tile
de Safe Zone, el objetivo está `HIDDEN` o `GARRISONED`, el dueño del objetivo o el del atacante está protegido
(`INV-CITY-006`; el del atacante desde E3, `RN-COMBAT-018`), o existe un tratado `ACTIVE` de no agresión o alianza entre los dos jugadores.

**Razón.** Son las promesas que el juego hace a quien se refugia, se esconde, se desconecta o firma la paz. Un
solo golpe que las ignore las vuelve falsas.

**Cómo se garantiza.** `DOMAIN` — la orden se valida al llegar y **otra vez** en cada golpe de la fase 4, con el
mismo predicado: el estado puede cambiar durante la persecución. La respuesta automática de E3
([../specs/combat-response.md](../specs/combat-response.md)) pasa por el mismo predicado antes de abrirse. Desde E5,
el golpe de saqueo sobre una ciudad también (`RN-COMBAT-020`): la ciudad cuenta como objetivo y su dueño como el
de la víctima. Desde E8, el golpe de asedio sobre una casa pasa por ese mismo predicado (`RN-COMBAT-030`), con la
ciudad de la casa como objetivo.

**Cómo se verifica.** `TestNingunGolpeCaeSobreUnaUnidadEnSafeZone`, `TestRechazosDelAtaque` (objetivo oculto, dueño
protegido, tratado) y `TestLaProteccionAMitadDePersecucionTerminaElEnfrentamiento` (la revalidación en cada
golpe), y desde E3 `TestUnIncursorProtegidoDejaDeGolpear` y `TestUnJugadorProtegidoNoPuedeAtacar` (la protección del
atacante, `RN-COMBAT-018`) — **existen y pasan**, en `internal/game/simulation`. Desde E8, `TestRechazosDelAsedio` y
`TestLaProteccionAMitadDelAsedioLoTermina` (la revalidación en cada golpe de asedio; quitarla lo pone en rojo).

**Violación en runtime.** No hay ruta conocida; un golpe que llegara a evaluarse en esas condiciones se
descarta y se registra `invariant_violation` con `inv_id=INV-COMBAT-002`.

**Precisión (2026-11-13, [SPEC-CARAVANS](../specs/caravans.md) §4.4 y Q7, [ADR-037](../decisions/ADR-037-caravans-as-units.md) §6).**
La protección del dueño del objetivo no cuenta cuando el objetivo es una caravana: una caravana se puede golpear
aunque su dueño esté protegido. El resto del enunciado vale también para ella (Safe Zone bajo el atacante o bajo la
caravana, tratado de paz, protección del atacante), y la excepción vive en el mismo predicado, revalidado en cada
golpe. `TestLaProteccionDelDuenoNoCubreSuCaravana` lo comprueba (a un aldeano protegido, no; a su caravana, sí; con un
pacto, tampoco) — **existe y pasa**. Verificado por mutación: sin la rama de la caravana, se pone en rojo.

---

<a id="inv-combat-003"></a>
## INV-COMBAT-003 — Un combate o un saqueo nunca crea madera

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | E2 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestElBotinPasaAlAsesinoYSeDeposita` (`internal/game/simulation`) y `TestElVolcadoEscribeVidaYBotinYNoResucita` (integration); el saqueo, por `TestElSaqueoGoldenCuatroGolpesYDeposito`, `TestLaCargaPendienteNoSeVuelcaNiSeHereda`, `TestElVolcadoEscribeAlSaqueadorIdleYSinLaCargaPendiente` (`internal/game/simulation`) y `TestUnGolpeDeSaqueoQueFallaNoDejaNadaAMedias` (integration) |

**Enunciado.** En toda muerte, lo que gana el atacante es como mucho lo que llevaba la víctima; y la madera que
un `WARRIOR` deposita en su ciudad es exactamente la que llevaba, que pasa a cero. Desde E5: en todo golpe de
saqueo, lo que gana el guerrero es exactamente lo que pierde el almacén de la víctima, y en disco el débito llega
antes que la carga ([ADR-026](../decisions/ADR-026-pillage-two-phase-stock-debit.md)). **Precisión (2026-11-17, [SPEC-CIVILIZATION-UNITS](../specs/civilization-units.md)):** desde E22, «`WARRIOR`» y «guerrero» se leen aquí como toda unidad que ataca, también las cuatro de civilización (`RN-CIVUNIT-005`); el enunciado no cambia.

**Razón.** El botín es una transferencia, no una fuente. Si matar creara madera, el combate sería una granja.

**Cómo se garantiza.** `DOMAIN` — el botín es `min(carga de la víctima, hueco del atacante)`. La transacción de
muerte deja a la víctima sin carga; la carga del asesino viaja en el volcado de su unidad, así que una caída
puede perderla pero no duplicarla. El depósito resta del guerrero lo que suma a la ciudad.

**Cómo se verifica.** `TestElBotinPasaAlAsesinoYSeDeposita` (unit) — **existe y pasa**: con cinco de hueco gana
cinco y se pierden cinco, y al depósito llega exactamente lo que lleva. `TestElVolcadoEscribeVidaYBotinYNoResucita`
(integration) — **existe y pasa**.

**Violación en runtime.** Se registra `invariant_violation` con `inv_id=INV-COMBAT-003` y no se aplica la
transferencia.

---

<a id="inv-combat-004"></a>
## INV-COMBAT-004 — Ningún saqueo deja el almacén de una ciudad por debajo del suelo

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | E5 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestElSaqueoRespetaElSuelo` y `TestDosSaqueadoresSeRepartenLoQueQuedaPorEncimaDelSuelo` (`internal/game/simulation`) |

**Enunciado.** Tras cualquier golpe de saqueo, el stock de madera de la ciudad saqueada es mayor o igual que
`EO_PILLAGE_STOCK_FLOOR`, o el que tenía antes del golpe si ya estaba por debajo. Los encargos de su dueño sí pueden
bajarlo.

**Razón.** Una ciudad saqueada siempre puede volver a producir (SPEC-COMBAT-PILLAGE Q3). Sin suelo, un jugador
grande dejaría a uno pequeño a cero en cada desconexión.

**Cómo se garantiza.** `DOMAIN` — lo robado es `min(madera por golpe, hueco de la carga, stock − suelo)`, calculado
en el instante de aplicar el golpe contra el stock que dejaron los anteriores del mismo tick (`RN-COMBAT-022`).

**Cómo se verifica.** `TestElSaqueoRespetaElSuelo` (con 57 roba 5 y 2, y para en 50; con el almacén ya en el suelo,
la orden se acepta y no roba nada) y `TestDosSaqueadoresSeRepartenLoQueQuedaPorEncimaDelSuelo` (con 58, 5 y 3, en orden
de guerrero) — **existen y pasan**, en `internal/game/simulation`. Quitar el suelo del cálculo los pone en rojo.

**Violación en runtime.** Un golpe que fuera a dejar el stock por debajo no se aplica y se registra
`invariant_violation` con `inv_id=INV-COMBAT-004`.

---

<a id="inv-combat-005"></a>
## INV-COMBAT-005 — Ningún golpe de saqueo o de asedio cae con el dueño del atacante ausente

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | E5 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestElSaqueoTerminaConElDuenoAusente` (`internal/game/simulation`) |

**Enunciado.** Un golpe de saqueo o de asedio sólo se aplica si el dueño del guerrero está presente: su ciudad `ONLINE` y su
sesión viva o dentro del margen de reconexión, el mismo predicado que
[INV-ECON-006](economy.md#inv-econ-006) aplica a los viajes de recolección.

**Razón.** Saquear es una acción del jugador presente (SPEC-COMBAT-PILLAGE Q4). Sin esta regla bastaría dejar
guerreros pegados a una muralla y apagar el ordenador. El asedio la hereda (SPEC-COMBAT-SIEGE Q6).

**Cómo se garantiza.** `DOMAIN` — la fase 4 comprueba la presencia antes de cada golpe de saqueo y, si falta,
termina el saqueo (`RN-COMBAT-025`). Desde E8 hace lo mismo antes de cada golpe de asedio (`RN-COMBAT-035`,
`RN-COMBAT-036`).

**Cómo se verifica.** `TestElSaqueoTerminaConElDuenoAusente` — **existe y pasa**: dentro del margen de reconexión el
saqueo sigue; con la ciudad del saqueador `OFFLINE_PENDING`, termina y no hay más golpes. Quitar la comprobación lo
pone en rojo. Desde E8, `TestElAsedioTerminaConElDuenoAusente` hace lo mismo con el asedio, y quitar la comprobación
también lo pone en rojo.

**Violación en runtime.** No hay ruta conocida; un golpe que llegara a evaluarse con el dueño ausente se descarta
y se registra `invariant_violation` con `inv_id=INV-COMBAT-005`.

---

<a id="inv-combat-006"></a>
## INV-COMBAT-006 — Ningún golpe, robo, derribo ni avance de estandarte se aplica entre dos jugadores de la Orden

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | E17 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestLaTablaDeLosBandos`, `TestDosDeLaOrdenDejanDePelearAlEntrarElSegundo` y `TestLaOrdenNoSaqueaNiConquistaALosSuyos` (`internal/game/simulation`), y `TestUnirseALaOrdenPorLaRed` (e2e, `internal/websocket`) |

**Enunciado.** En un tick en que los dos jugadores son de `ORDER` (con la facción efectiva, no la pendiente), no se
aplica ningún golpe de una unidad de uno a una unidad del otro (también a una caravana), ningún golpe de saqueo ni de
asedio de uno contra la ciudad del otro, y ningún estandarte de uno avanza sobre un territorio del otro.

**Razón.** Es la regla que define a la Orden (SPEC-FACTIONS Q4). Un solo golpe que se cuele —un enfrentamiento que
empezó antes de la entrada y no termina, una captura de caravana— rompería la promesa del bando, que es lo que el
jugador eligió para siempre.

**Cómo se garantiza.** `DOMAIN` — `State.AtPeace` responde que sí entre dos de `ORDER` (ADR-039 §2), y lo consultan la
orden de las cuatro acciones hostiles y cada tick de combate, saqueo, asedio y conquista, que terminan lo que esté en
curso en el tick en que la paz aparece (`RN-COMBAT-008`, `RN-COMBAT-025`).

**Cómo se verifica.** **Existen y pasan**: `TestLaTablaDeLosBandos` recorre la tabla de §4.1 de la spec celda a
celda (`AtPeace` en los dos sentidos); `TestDosDeLaOrdenDejanDePelearAlEntrarElSegundo` pone a un guerrero de la Orden
a golpear a un aldeano de un `NEUTRAL` cuya entrada en la Orden cuenta a mitad del combate, y comprueba que el
enfrentamiento termina en ese tick sin un golpe más y que la orden siguiente se rechaza «los dos sois de la Orden»;
`TestLaOrdenNoSaqueaNiConquistaALosSuyos` rechaza con el mismo motivo el saqueo, el asedio de una casa y la reclamación de
un territorio del otro; y `TestUnirseALaOrdenPorLaRed` lo
hace de punta a punta por WebSocket. Verificado por mutación: sin la Orden en `AtPeace`, falla la tabla; sin ella
además en la validación del ataque, falla el combate en curso. La captura de una caravana es un `unit.attack` y pasa
por la misma validación.

**Violación en runtime.** Un golpe que llegara a evaluarse entre dos de la Orden se descarta y se registra
`invariant_violation` con `inv_id=INV-COMBAT-006`.

---

<a id="inv-combat-007"></a>
## INV-COMBAT-007 — Un castillo sólo golpea a quien, a su alcance, ataca algo de su dueño, y nunca en paz

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | E23 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestElCastilloSoloGolpeaAQuienAtacaASuDueno`, `TestElCastilloGoldenMataAlGuerreroQueAsedia` y `TestElCastilloEligeAlMasCercanoYDesempataPorID` (`internal/game/simulation`), y `TestElCastilloDisparaPorLaRed` (e2e) |

**Enunciado.** Todo golpe de un castillo que se aplica en un tick cae sobre una unidad viva, en el mapa, a distancia de
Chebyshev no mayor que el alcance del castillo, que en ese tick está en `ATTACKING` contra una unidad del dueño del
castillo, contra su ciudad (saqueo) o contra uno de sus edificios (asedio); y nunca sobre una unidad de un jugador en
paz con el dueño del castillo, oculta, sobre una Safe Zone o de un jugador `PROTECTED`, ni con el dueño del castillo
`PROTECTED`.

**Razón.** El castillo es una defensa que reacciona a un ataque (SPEC-CASTLE-AGE Q5): si golpeara a quien pasa, a un
aliado o a un compañero de clan, rompería la paz que el jugador eligió y haría de cada ciudad un peaje.

**Cómo se garantiza.** `DOMAIN` — el objetivo sale de los enfrentamientos en `ATTACKING` cuyo blanco es del dueño y pasa
`castleBlocked`, el predicado de hostilidad del castillo (las cuatro fuentes de paz, la protección de los dos lados, la
visibilidad y las Safe Zones), que se vuelve a comprobar antes de aplicar cada golpe
([ADR-044](../decisions/ADR-044-castle-shooting.md) §3 y §4).

**Cómo se verifica.** `TestElCastilloGoldenMataAlGuerreroQueAsedia` y `TestElCastilloEligeAlMasCercanoYDesempataPorID`:
quien asedia o ataca a una unidad del dueño a su alcance recibe, el más cercano primero. `TestElCastilloSoloGolpeaAQuienAtacaASuDueno`:
quien pasa sin atacar, quien ataca a otro jugador, quien ataca fuera del alcance y quien queda en paz a mitad del ataque
no reciben nada, ni un castillo en obra dispara. Verificado por mutación el 2026-11-17: sin la comprobación del dueño del
blanco falla «ataca a otro jugador»; con el alcance ampliado en dos, «fuera de su alcance». La paz a mitad del ataque
termina antes el enfrentamiento (`RN-COMBAT-008`), así que la comprobación de `castleBlocked` al aplicar queda como
defensa.

**Violación en runtime.** Un golpe de castillo que llegara a aplicarse sobre una unidad que no pasa el predicado se
descarta y se registra `invariant_violation` con `inv_id=INV-COMBAT-007`.

---

<a id="inv-combat-008"></a>
## INV-COMBAT-008 — Con la muralla en pie, sólo el ariete daña la ciudad, y sólo a la muralla

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | E27 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestQuienDanaQue` (`internal/domain/siege`), `TestGoldenTresArietesAbrenLaMuralla`, `TestLaMurallaQueSeCompletaAMitadDetieneElDerribo` y `TestUnaMurallaEnObraSeDerribaComoUnaCasa` (`internal/game/simulation`), y `TestElArieteAbreLaMurallaPorLaRed` (e2e) |

**Enunciado.** Mientras una ciudad tiene una muralla `COMPLETE` con vida, ningún golpe de asedio de una unidad que no
sea un ariete resta vida a uno de sus edificios, y ningún golpe de un ariete resta vida a otro edificio de esa ciudad
que su muralla. Con la ciudad en brecha (sin muralla, con la muralla en obra o derribada), la regla de E8 no cambia.

**Razón.** La muralla es la inversión que compra protección (SPEC-SIEGE-ENGINES Q4): si un guerrero pudiera derribar una
casa de una ciudad amurallada, o un ariete saltarse la muralla, la piedra pagada no compraría nada y el asedio no
pediría máquina.

**Cómo se garantiza.** `DOMAIN` — un solo predicado, `siege.CanDamage`, que llaman la orden `unit.demolish` y cada golpe
de asedio de la fase 4 antes de aplicarlo; un asedio que el predicado deja de admitir (la muralla se completa a mitad)
termina en ese tick ([ADR-048](../decisions/ADR-048-siege-engines-and-wall.md) §2, `RN-SIEGE-004`, `RN-SIEGE-006`).

**Cómo se verifica.** `TestQuienDanaQue` recorre la tabla entera de §4.3 (cada tipo de unidad contra cada edificio,
amurallada y en brecha); el golden `TestGoldenTresArietesAbrenLaMuralla`, que con la muralla en pie el guerrero y el
ariete no van a por la casa y que tras el derribo el guerrero sí; `TestLaMurallaQueSeCompletaAMitadDetieneElDerribo`,
que el derribo de una casa se corta al completarse la muralla; `TestElArieteAbreLaMurallaPorLaRed`, el rechazo por la
red — **existen y pasan**. Verificado por mutación el 2026-11-19: `siege.Check` sin mirar la muralla tumba
`TestQuienDanaQue`, el golden y el de la muralla que se completa; quitar el predicado de la orden tumba el golden; quitar
el de la fase 4 (en `stepSiege` y en la defensa de `applySiege`, a la vez: cada uno cubre al otro) tumba el de la muralla
que se completa.

**Violación en runtime.** Un golpe de asedio que llegara a aplicarse sin pasar el predicado se descarta y se registra
`invariant_violation` con `inv_id=INV-COMBAT-008`.

---

<a id="inv-combat-009"></a>
## INV-COMBAT-009 — Un ariete nunca hiere a una unidad ni roba

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | E27 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestElArieteSoloGolpeaEdificios` y `TestElGolpeDeAsedioDeQuienAtaca` (`internal/domain/unit`), `TestElArieteSoloDerriba` (`internal/game/simulation`) y `TestElCatalogoDeUnidadesEsElDePresentacion` (contract) |

**Enunciado.** Un ariete nunca resta vida a una unidad, nunca carga botín de un saqueo ni de una muerte, y nunca
captura una caravana ni un territorio. Sólo golpea edificios.

**Razón.** El ariete es una máquina que sólo derriba (SPEC-SIEGE-ENGINES Q7); su única defensa es la escolta. Si pudiera
herir o robar sería una unidad de combate más fuerte y más barata por punto de vida que el guerrero.

**Cómo se garantiza.** `DOMAIN` — su catálogo tiene a cero el golpe de combate y el botín, así que `CanAttack()` es
falso y `unit.attack`, `unit.pillage`, `unit.claim`, la respuesta automática y la elección de objetivos de un
enfrentamiento lo excluyen sin un caso especial; `unit.demolish` pregunta `CanDemolish()`
([ADR-048](../decisions/ADR-048-siege-engines-and-wall.md) §3, `RN-SIEGE-003`).

**Cómo se verifica.** `TestElArieteSoloGolpeaEdificios` (sin golpe de combate ni botín, `CanAttack()` falso);
`TestElArieteSoloDerriba` (los cinco rechazos con `UNIT_NOT_MOVABLE`, la víctima intacta, y el ariete del saqueado que
no responde) — **existen y pasan**. Verificado por mutación el 2026-11-19: dar al ariete un golpe de combate tumba esos
dos, el contract del catálogo de presentación y los de simulación que cuentan sus golpes; quitar el rechazo de la
guarnición tumba `TestElArieteSoloDerriba`.

**Violación en runtime.** Un golpe de combate de un ariete que llegara a resolverse se descarta y se registra
`invariant_violation` con `inv_id=INV-COMBAT-009`.

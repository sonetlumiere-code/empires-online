# Invariantes de Combate (INV-COMBAT-xxx)

Propiedades del combate: la vida de una unidad, la muerte, el botín, las reglas que impiden un golpe y, desde E5,
el saqueo del almacén de una ciudad.

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
| Cobertura | **Cubierto** por `TestNingunGolpeCaeSobreUnaUnidadEnSafeZone`, `TestRechazosDelAtaque`, `TestLaProteccionAMitadDePersecucionTerminaElEnfrentamiento`, `TestUnIncursorProtegidoDejaDeGolpear` y `TestUnJugadorProtegidoNoPuedeAtacar` (`internal/game/simulation`) |

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
atacante, `RN-COMBAT-018`) — **existen y pasan**, en `internal/game/simulation`. El asedio, previsto con E8: rechazos
de `unit.demolish` y revalidación en cada golpe.

**Violación en runtime.** No hay ruta conocida; un golpe que llegara a evaluarse en esas condiciones se
descarta y se registra `invariant_violation` con `inv_id=INV-COMBAT-002`.

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
antes que la carga ([ADR-026](../decisions/ADR-026-pillage-two-phase-stock-debit.md)).

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
pone en rojo. El asedio, previsto con E8: termina al ausentarse el dueño del atacante.

**Violación en runtime.** No hay ruta conocida; un golpe que llegara a evaluarse con el dueño ausente se descarta
y se registra `invariant_violation` con `inv_id=INV-COMBAT-005`.

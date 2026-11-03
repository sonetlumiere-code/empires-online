# Invariantes de Combate (INV-COMBAT-xxx)

Propiedades del primer slice de combate: la vida de una unidad, la muerte, el botín y las reglas que impiden
un golpe.

Formato y severidades: [README.md](README.md). Unidades: [units.md](units.md). Ciudades: [city.md](city.md).
Economía: [economy.md](economy.md). Spec: [../specs/combat-raid.md](../specs/combat-raid.md). Decisión:
[ADR-024](../decisions/ADR-024-combat-simultaneous-resolution.md).

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
| Cobertura | **Cubierto** por `TestNingunGolpeCaeSobreUnaUnidadEnSafeZone`, `TestRechazosDelAtaque` y `TestLaProteccionAMitadDePersecucionTerminaElEnfrentamiento` (`internal/game/simulation`) |

**Enunciado.** Ningún golpe se aplica si, en el instante del golpe, el atacante o el objetivo están sobre un tile
de Safe Zone, el objetivo está `HIDDEN` o `GARRISONED`, el dueño del objetivo o el del atacante está protegido
(`INV-CITY-006`; el del atacante desde E3, `RN-COMBAT-018`), o existe un tratado `ACTIVE` de no agresión o alianza entre los dos jugadores.

**Razón.** Son las promesas que el juego hace a quien se refugia, se esconde, se desconecta o firma la paz. Un
solo golpe que las ignore las vuelve falsas.

**Cómo se garantiza.** `DOMAIN` — la orden se valida al llegar y **otra vez** en cada golpe de la fase 4, con el
mismo predicado: el estado puede cambiar durante la persecución. La respuesta automática de E3
([../specs/combat-response.md](../specs/combat-response.md)) pasa por el mismo predicado antes de abrirse.

**Cómo se verifica.** `TestNingunGolpeCaeSobreUnaUnidadEnSafeZone`, `TestRechazosDelAtaque` (objetivo oculto, dueño
protegido, tratado) y `TestLaProteccionAMitadDePersecucionTerminaElEnfrentamiento` (la revalidación en cada
golpe) — **existen y pasan**, en `internal/game/simulation`.

**Violación en runtime.** No hay ruta conocida; un golpe que llegara a evaluarse en esas condiciones se
descarta y se registra `invariant_violation` con `inv_id=INV-COMBAT-002`.

---

<a id="inv-combat-003"></a>
## INV-COMBAT-003 — Un combate nunca crea madera

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | E2 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestElBotinPasaAlAsesinoYSeDeposita` (`internal/game/simulation`) y `TestElVolcadoEscribeVidaYBotinYNoResucita` (integration) |

**Enunciado.** En toda muerte, lo que gana el atacante es como mucho lo que llevaba la víctima; y la madera que
un `WARRIOR` deposita en su ciudad es exactamente la que llevaba, que pasa a cero.

**Razón.** El botín es una transferencia, no una fuente. Si matar creara madera, el combate sería una granja.

**Cómo se garantiza.** `DOMAIN` — el botín es `min(carga de la víctima, hueco del atacante)`. La transacción de
muerte deja a la víctima sin carga; la carga del asesino viaja en el volcado de su unidad, así que una caída
puede perderla pero no duplicarla. El depósito resta del guerrero lo que suma a la ciudad.

**Cómo se verifica.** `TestElBotinPasaAlAsesinoYSeDeposita` (unit) — **existe y pasa**: con cinco de hueco gana
cinco y se pierden cinco, y al depósito llega exactamente lo que lleva. `TestElVolcadoEscribeVidaYBotinYNoResucita`
(integration) — **existe y pasa**.

**Violación en runtime.** Se registra `invariant_violation` con `inv_id=INV-COMBAT-003` y no se aplica la
transferencia.

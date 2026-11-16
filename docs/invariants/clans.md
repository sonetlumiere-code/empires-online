# Invariantes de Clanes (INV-CLAN-xxx)

Propiedades de los clanes: quién pertenece a cuál, quién lo dirige, qué bandos caben en uno y la paz entre sus
miembros.

Formato y severidades: [README.md](README.md). Jugadores: [player.md](player.md). Combate: [combat.md](combat.md).
Diplomacia: [diplomacy.md](diplomacy.md). Seguridad del canal: [security.md](security.md). Spec:
[../specs/clans.md](../specs/clans.md). Decisión: [ADR-040](../decisions/ADR-040-clans-membership-and-peace.md).
Territorios de clan: [../specs/clan-territories.md](../specs/clan-territories.md) y
[ADR-041](../decisions/ADR-041-clan-territories.md). Gobierno del clan:
[../specs/clan-governance.md](../specs/clan-governance.md).

---

<a id="inv-clan-001"></a>
## INV-CLAN-001 — Un jugador está en un clan como mucho, y todo clan vivo tiene un único líder que es miembro

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E18 |
| Política ante violación | REJECT (escritura) · FAIL_FAST (arranque) |
| Cobertura | **Cubierto**: la DB por `TestUnaEstanciaAbiertaPorJugador` (índice único y validación de carga) y `TestLaPertenenciaAUnClanEnLaBase` (líder heredado y disolución en la transacción de salida), integration en `internal/persistence/postgres`; el dominio por `TestElLiderazgoYLaDisolucion` (`internal/game/simulation`) y `TestElLiderazgoPasaAlMasAntiguo` (`internal/domain/clan`); desde E20, el traspaso y la sucesión por `TestElCambioDeLiderEnLaBase` (integration), `TestCederElLiderazgo`, `TestGoldenLiderAusente` y `TestAlSalirElLiderHeredaElOficialMasAntiguo` (`internal/game/simulation`) |

**Enunciado.** Para todo jugador hay como mucho una fila de `clan_members` con `left_at IS NULL`; y para todo clan
con `dissolved_at IS NULL`, `leader_player_id` es el jugador de una de sus filas abiertas, y el clan tiene al menos
una. Lo mismo en la RAM del Game Server.

**Razón.** Dos clanes a la vez darían paz con dos grupos que pueden estar en guerra entre sí, y un clan sin líder (o
con un líder que ya no está) no puede invitar ni expulsar a nadie: queda congelado para siempre
([SPEC-CLANS](../specs/clans.md) `RN-CLAN-001`, `RN-CLAN-007`).

**Cómo se garantiza.** `DB` — un índice único parcial sobre `clan_members (player_id) WHERE left_at IS NULL`
(migración `000020`). `DOMAIN` — el tick rechaza fundar o aceptar a quien ya está en un clan o tiene una reserva en
vuelo; la salida del líder pasa el liderazgo al miembro más antiguo y la del último disuelve el clan, en la misma
transacción que cierra la estancia ([ADR-040](../decisions/ADR-040-clans-membership-and-peace.md) §2). La validación
de carga lo comprueba al arrancar.

**Cómo se verifica.** En la integración, el índice que rechaza una segunda estancia abierta, la validación de carga
que encuentra un clan con un líder que no es miembro, y la transacción de salida que deja el líder heredado o el clan
disuelto, idempotente; en la simulación, el paso del liderazgo con su desempate, la disolución del último y que la
última salida espera a una entrada en vuelo.

**Violación en runtime.** La validación de carga que encuentre dos estancias abiertas de un jugador o un clan vivo
sin líder abierto registra `invariant_violation` con `inv_id=INV-CLAN-001` y no arranca.

**Precisión (2026-11-16, [SPEC-CLAN-GOVERNANCE](../specs/clan-governance.md)).** El enunciado no cambia. Con E20 el
líder cambia también por `clan.transfer` y por la sucesión por ausencia, y la salida del líder pasa el liderazgo al
oficial más antiguo antes que al miembro más antiguo (`RN-CLANGOV-004`). Cada uno de esos cambios deja
`leader_player_id` en una estancia abierta en la misma transacción, con el candado del clan.

---

<a id="inv-clan-002"></a>
## INV-CLAN-002 — Ningún clan junta a la Orden y al Caos

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E18 |
| Política ante violación | REJECT (escritura) · FAIL_FAST (arranque) |
| Cobertura | **Cubierto**: la DB por `TestLosTriggersNoJuntanALaOrdenYAlCaos` (integration, los dos triggers; verificado por mutación: con `clan_members_faction_guard` desactivado, el test falla); el dominio por `TestUnClanNoJuntaALaOrdenYAlCaos` en `internal/game/simulation` (invitar, aceptar con una entrada pendiente y `faction.join`) y en `internal/domain/clan` |

**Enunciado.** Ningún clan vivo tiene a la vez, entre sus miembros abiertos, uno cuya facción o entrada pendiente en
un bando sea `ORDER` y otro cuya facción o entrada pendiente sea `CHAOS`.

**Razón.** La paz del clan entre la Orden y el Caos daría a los dos bandos la paz que
[SPEC-FACTIONS](../specs/factions.md) `RN-FAC-005` les prohíbe ([INV-DIPLO-003](diplomacy.md#inv-diplo-003)), por la
puerta de atrás ([SPEC-CLANS](../specs/clans.md) Q7).

**Cómo se garantiza.** `DOMAIN` — el tick lo comprueba al invitar, al aceptar y en `faction.join` de un miembro,
contando la facción efectiva, las entradas pendientes y las reservas en vuelo. `DB` — dos triggers de `000020`, al
abrir una estancia y al apuntar una entrada o cambiar la facción en `players`, rechazan con `23514` y toman la fila
del clan para que dos escrituras no se crucen ([ADR-040](../decisions/ADR-040-clans-membership-and-peace.md) §4).

**Cómo se verifica.** En la simulación, un rechazo por cada camino (invitar, aceptar con una entrada pendiente y
`faction.join` de un miembro); en la integración, cada trigger rechazando su escritura, y la entrada por el
repositorio rechazada con su motivo. La carrera entre dos transacciones concurrentes la cubre el `FOR UPDATE` de la
fila del clan en los dos triggers; no tiene un test propio.

**Violación en runtime.** La validación de carga que encuentre un clan con los dos bandos registra
`invariant_violation` con `inv_id=INV-CLAN-002` y no arranca.

---

<a id="inv-clan-003"></a>
## INV-CLAN-003 — Ningún golpe, robo, derribo ni avance de estandarte se aplica entre compañeros de clan, ni durante la paz residual

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | E18 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestDosDelMismoClanDejanDePelear` (verificado por mutación: con `AtPeace` sin la paz del clan, el test falla), `TestLaPazResidualTrasSalir` y `TestLosClanesVuelvenAlArrancar` (`internal/game/simulation`), `TestLaPazResidual` (`internal/domain/clan`) y el e2e `TestUnClanPorLaRed` (`internal/websocket`) |

**Enunciado.** En un tick en que dos jugadores son miembros abiertos del mismo clan, o en que uno salió de un clan
hace menos de `EO_CLAN_PEACE_GRACE_SECONDS` y el otro era miembro de ese clan en el instante de la salida, no se
aplica ningún golpe de una unidad de uno a una unidad del otro (también a una caravana), ningún golpe de saqueo ni de
asedio de uno contra la ciudad del otro, y ningún estandarte de uno avanza sobre un territorio del otro.

**Razón.** Es lo que da sentido al clan ([SPEC-CLANS](../specs/clans.md) Q1) y lo que impide usar la salida o la
expulsión para atacar por sorpresa a quien tienes al lado (Q6). Un solo golpe que se cuele rompe la confianza en la
regla, que es lo que el jugador aceptó al entrar.

**Cómo se garantiza.** `DOMAIN` — `State.AtPeace` responde que sí por el clan y por la paz residual, derivada de las
fechas de entrada y salida ([ADR-040](../decisions/ADR-040-clans-membership-and-peace.md) §3), y lo consultan la
orden de las cuatro acciones hostiles y cada tick de combate, saqueo, asedio y conquista, que terminan lo que esté en
curso en el tick en que la paz aparece (`RN-COMBAT-008`, `RN-COMBAT-025`).

**Cómo se verifica.** En la simulación, un enfrentamiento que para en el tick en que se confirma la entrada, sin un
golpe más, y el ataque rechazado con su motivo; un ex miembro que no puede atacar a un antiguo compañero hasta que
vence su paz residual, al milisegundo, y con quien entró después de su salida no tiene paz; en la recuperación, una
paz residual que vuelve con la carga y vence a su hora. El saqueo, el asedio y la conquista pasan por el mismo
`AtPeace` y su predicado de motivo (`ClanPeace`), como en la paz de la Orden.

**Violación en runtime.** Un golpe que llegara a evaluarse entre dos jugadores en paz de clan se descarta y se
registra `invariant_violation` con `inv_id=INV-CLAN-003`.

---

<a id="inv-clan-004"></a>
## INV-CLAN-004 — Una reclamación para un clan sólo se completa mientras quien la sostiene es miembro de ese clan

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E19 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestQueRompeUnaReclamacionConUnClan` y `TestUnaReclamacionParaElClanTrasUnReinicio` (`internal/game/simulation`; verificado por mutación: sin la comprobación de la pertenencia en el barrido, los dos fallan) y `TestLaCapturaParaUnClanExigeSerDeEl` (integration; verificado por mutación: sin la comprobación de la estancia en la transacción, falla) |

**Enunciado.** Una reclamación hecha para un clan avanza sólo en los ticks en que el dueño del guerrero que la
sostiene es miembro abierto de ese clan; en el tick en que deja de serlo termina sin captura. Ninguna captura escribe
como dueño un clan del que quien reclamó no tiene, en esa transacción, una estancia abierta.

**Razón.** Reclamar para el clan es actuar en su nombre ([SPEC-CLAN-TERRITORIES](../specs/clan-territories.md) Q1 y
Q2), y ese derecho es de los miembros. Sin esta regla, quien sale o es expulsado podría dejar un estandarte que
seguiría ganando tierra para un clan que ya no es el suyo, o completar la captura de un clan disuelto.

**Cómo se garantiza.**

- `DOMAIN` — el predicado de la fase 5 de una reclamación para el clan exige la pertenencia abierta de su dueño en la
  RAM, y una salida, una expulsión o una disolución la rompen en ese tick
  ([ADR-041](../decisions/ADR-041-clan-territories.md) §1).
- `DB` — la transacción de la captura lee la estancia abierta con el candado del clan y, sin ella, no escribe nada y
  vuelve como conflicto (ADR-041 §3).

**Cómo se verifica.** En la simulación, una reclamación para el clan que termina sin captura en el tick en que su
dueño sale, en el que lo expulsan y en el de la disolución; en la integración, una captura para un clan con la
estancia de quien reclamó cerrada no escribe nada; en la recuperación, una reclamación cuyo dueño salió del clan con
el servidor parado no se completa.

**Violación en runtime.** Rechazo: la reclamación termina sin cambio de dueño, o la captura no se escribe y la
reclamación termina como en un conflicto.

---

<a id="inv-clan-005"></a>
## INV-CLAN-005 — Sólo el rango que lo permite cambia la pertenencia ajena, los rangos o el liderazgo de un clan

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E20 |
| Política ante violación | REJECT |
| Cobertura | **Cubierto** por `TestLosPermisosPorRango` y `TestLoQueLaBaseNoEscribeSeDeshace` (`internal/game/simulation`), `TestLosPermisosDeCadaRango` (`internal/domain/clan`), y `TestElRangoSeCompruebaEnLaBase` y `TestElCambioDeLiderEnLaBase` (integration). Verificado por mutación: sin la comprobación del rango en la transacción de invitar, de expulsar, de nombrar o de ceder, falla la integración; con un oficial que expulsa a otro oficial, falla la simulación; sin deshacer una expulsión o un traspaso que la base no admite, falla la simulación; con cualquiera de los dos triggers de `000022` desactivado, falla la integración |

**Enunciado.** Ninguna invitación, retirada de invitación, expulsión, cambio de rango ni traspaso del liderazgo se
escribe si quien lo ordenó no tenía, en la transacción y con el candado del clan, el rango que lo permite: invitar,
retirar y expulsar a un miembro raso, el líder o un oficial abierto; expulsar a un oficial, nombrar o quitar oficiales
y ceder el liderazgo, sólo el líder. La sucesión automática (al salir el líder o por su ausencia) no tiene autor y
sigue `RN-CLANGOV-004` y `RN-CLANGOV-005`.

**Razón.** Con varios rangos, lo que hoy protege `leader_player_id` pasa a depender del rango de la estancia, que
cambia con órdenes optimistas ([SPEC-CLAN-GOVERNANCE](../specs/clan-governance.md) §4.3, §7). Sin comprobarlo en la
base, un oficial al que el líder acaba de quitar el rango, o un líder que acaba de ceder, podría colar una expulsión o
una invitación que el tick validó con el rango viejo.

**Cómo se garantiza.**

- `DOMAIN` — el tick valida cada orden contra el rango de la RAM (`RN-CLANGOV-002`, `RN-CLANGOV-003`).
- `DB` — la transacción de cada orden lee el rango de quien la dio (`clans.leader_player_id` y `clan_members.rank` de
  su estancia abierta) con el candado del clan, el mismo que toman los cambios de rango y de líder, y sin él no
  escribe nada (`RN-CLANGOV-006`).

**Cómo se verifica.** En la simulación, la tabla de permisos de §4.2 de la spec, orden a orden y rango a rango; en la
integración, un oficial que invita y expulsa a un raso pero no a otro oficial, un ex oficial que ya no invita y un ex
líder que ya no cede.

**Violación en runtime.** Rechazo: la orden no escribe nada y vuelve como `INVALID_TARGET`.

---

## Trazabilidad

| Invariante | Componente propietario | Relacionado con |
|---|---|---|
| INV-CLAN-001 | `internal/persistence/postgres`, `internal/game/simulation`, `internal/game/loadcheck` | [INV-PERSIST-005](persistence.md#inv-persist-005) |
| INV-CLAN-002 | `internal/game/simulation`, `internal/persistence/postgres` | [INV-DIPLO-003](diplomacy.md#inv-diplo-003), [INV-PLAYER-013](player.md#inv-player-013) |
| INV-CLAN-003 | `internal/game/simulation` | [INV-COMBAT-006](combat.md#inv-combat-006), [INV-COMBAT-002](combat.md#inv-combat-002) |
| INV-CLAN-004 | `internal/game/simulation`, `internal/persistence/postgres` | [INV-TERR-010](territory.md#inv-terr-010), [INV-TERR-012](territory.md#inv-terr-012) |
| INV-CLAN-005 | `internal/game/simulation`, `internal/persistence/postgres` | [INV-CLAN-001](#inv-clan-001), [INV-SEC-004](security.md#inv-sec-004) |

# Invariantes de Clanes (INV-CLAN-xxx)

Propiedades de los clanes: quién pertenece a cuál, quién lo dirige, qué bandos caben en uno y la paz entre sus
miembros.

Formato y severidades: [README.md](README.md). Jugadores: [player.md](player.md). Combate: [combat.md](combat.md).
Diplomacia: [diplomacy.md](diplomacy.md). Seguridad del canal: [security.md](security.md). Spec:
[../specs/clans.md](../specs/clans.md). Decisión: [ADR-040](../decisions/ADR-040-clans-membership-and-peace.md).

---

<a id="inv-clan-001"></a>
## INV-CLAN-001 — Un jugador está en un clan como mucho, y todo clan vivo tiene un único líder que es miembro

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E18 |
| Política ante violación | REJECT (escritura) · FAIL_FAST (arranque) |
| Cobertura | **Sin cobertura**: se registra con SPEC-CLANS antes de escribir su código (§8 del catálogo) |

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

**Cómo se verifica.** Previsto: en la integración, el índice que rechaza una segunda estancia abierta y la
transacción de salida que deja un líder abierto o el clan disuelto, con un fallo inyectado que no deja nada a medias;
en la simulación, el paso del liderazgo con su desempate y la disolución del último.

**Violación en runtime.** La validación de carga que encuentre dos estancias abiertas de un jugador o un clan vivo
sin líder abierto registra `invariant_violation` con `inv_id=INV-CLAN-001` y no arranca.

---

<a id="inv-clan-002"></a>
## INV-CLAN-002 — Ningún clan junta a la Orden y al Caos

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E18 |
| Política ante violación | REJECT (escritura) · FAIL_FAST (arranque) |
| Cobertura | **Sin cobertura**: se registra con SPEC-CLANS antes de escribir su código (§8 del catálogo) |

**Enunciado.** Ningún clan vivo tiene a la vez, entre sus miembros abiertos, uno cuya facción o entrada pendiente en
un bando sea `ORDER` y otro cuya facción o entrada pendiente sea `CHAOS`.

**Razón.** La paz del clan entre la Orden y el Caos daría a los dos bandos la paz que
[SPEC-FACTIONS](../specs/factions.md) `RN-FAC-005` les prohíbe ([INV-DIPLO-003](diplomacy.md#inv-diplo-003)), por la
puerta de atrás ([SPEC-CLANS](../specs/clans.md) Q7).

**Cómo se garantiza.** `DOMAIN` — el tick lo comprueba al invitar, al aceptar y en `faction.join` de un miembro,
contando la facción efectiva, las entradas pendientes y las reservas en vuelo. `DB` — dos triggers de `000020`, al
abrir una estancia y al apuntar una entrada o cambiar la facción en `players`, rechazan con `23514` y toman la fila
del clan para que dos escrituras no se crucen ([ADR-040](../decisions/ADR-040-clans-membership-and-peace.md) §4).

**Cómo se verifica.** Previsto: en la simulación, un rechazo por cada camino (invitar, aceptar, `faction.join`), con
una entrada pendiente y con una reserva en vuelo; en la integración, cada trigger rechazando su escritura y dos
transacciones concurrentes que no dejan el clan con los dos bandos.

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
| Cobertura | **Sin cobertura**: se registra con SPEC-CLANS antes de escribir su código (§8 del catálogo) |

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

**Cómo se verifica.** Previsto: en la simulación, dos jugadores que se enfrentaban, saqueaban, derribaban o plantaban
estandarte y paran en el tick en que se confirma la entrada; un ex miembro que no puede atacar a un antiguo compañero
hasta el tick en que vence su paz residual, y sí a quien entró después de su salida; en la recuperación, una paz
residual que sobrevive a un reinicio.

**Violación en runtime.** Un golpe que llegara a evaluarse entre dos jugadores en paz de clan se descarta y se
registra `invariant_violation` con `inv_id=INV-CLAN-003`.

---

## Trazabilidad

| Invariante | Componente propietario | Relacionado con |
|---|---|---|
| INV-CLAN-001 | `internal/persistence/postgres`, `internal/game/simulation`, `internal/game/loadcheck` | [INV-PERSIST-005](persistence.md#inv-persist-005) |
| INV-CLAN-002 | `internal/game/simulation`, `internal/persistence/postgres` | [INV-DIPLO-003](diplomacy.md#inv-diplo-003), [INV-PLAYER-013](player.md#inv-player-013) |
| INV-CLAN-003 | `internal/game/simulation` | [INV-COMBAT-006](combat.md#inv-combat-006), [INV-COMBAT-002](combat.md#inv-combat-002) |

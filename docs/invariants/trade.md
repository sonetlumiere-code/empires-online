# Invariantes de Comercio (INV-TRADE-xxx)

Propiedades del comercio entre dos ciudades: lo que un trato mueve, quién puede cerrar una oferta y cuántas puede
haber entre dos jugadores.

Formato y severidades: [README.md](README.md). Stock: [economy.md](economy.md). Tratados:
[diplomacy.md](diplomacy.md). Spec: [../specs/trade.md](../specs/trade.md). Decisiones:
[ADR-023](../decisions/ADR-023-economy-stock-deltas-and-two-phase-orders.md),
[ADR-027](../decisions/ADR-027-hot-treaties.md), [ADR-032](../decisions/ADR-032-trade-offers-and-two-key-jobs.md).

---

## Contexto: el comercio, de extremo a extremo

| Pieza | Dónde | Qué hace |
|---|---|---|
| Reglas | `internal/domain/trade`, `internal/domain/diplomacy` | La oferta y sus líneas, quién puede responderla o retirarla; la escalera de tratados y qué tratado deja comerciar. No escriben ni conocen el protocolo. |
| Simulación | `internal/game/simulation/trade.go` | Las órdenes en dos fases contra la RAM; la reserva de los dos almacenes al aceptar; la caducidad en la fase 5. |
| Cola | `internal/persistence/queue.go` | El trabajo de dos claves: el trato espera a las dos ciudades (ADR-032 §3). |
| Persistencia | `postgres.TradeRepo` | Cada transición de una oferta con su evento; el trato aceptado, con los deltas de los dos almacenes, en una transacción. |

### Lo que la base de datos garantiza y lo que no

| Constraint | Tabla | Qué impone |
|---|---|---|
| `trade_offers_status_valid` | `trade_offers` | Dominio cerrado de `status` |
| `trade_offers_distinct_players` | `trade_offers` | `proposer_id <> target_id` |
| `trade_offers_one_pending_per_pair` | `trade_offers` | Índice **parcial** único sobre el par sin orden `WHERE status = 'PROPOSED'` |
| `trade_offers_unique_request` | `trade_offers` | Único parcial `(proposer_id, request_id)`: una orden no produce dos ofertas |
| `trade_offer_lines_pkey` | `trade_offer_lines` | `PRIMARY KEY (offer_id, resource)`: un recurso en un solo lado |
| `trade_offer_lines_amount_positive`, `trade_offer_lines_resource_valid`, `trade_offer_lines_side_valid` | `trade_offer_lines` | `amount > 0`, el recurso del catálogo y `side` en `GIVE` o `WANT` |

> **Lo que el esquema NO garantiza.** Que una oferta aceptada tuviera un tratado vigente cruza `trade_offers` y
> `treaties`, y lo decide la RAM, que es la fuente de verdad de los tratados (ADR-027 §1); que lo que sale de una
> ciudad entre en la otra cruza dos filas de `city_stocks`, y lo garantiza la transacción del trato. Ninguna ficha de
> esta familia declara `DB` para ellas.

---

<a id="inv-trade-001"></a>
## INV-TRADE-001 — Un trato nunca crea ni destruye recursos

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E10 |
| Política ante violación | REJECT (la transacción revierte entera; la RAM se compensa) |
| Cobertura | **Sin cobertura**: registrado el 2026-11-04 con SPEC-TRADE, antes de escribir su código |

**Enunciado.** Para todo trato aceptado y para cada recurso, la cantidad que resta del almacén de una ciudad es
exactamente la que suma al de la otra, y las dos escrituras se confirman en la misma transacción que pasa la oferta a
`ACCEPTED`.

**Razón.** Un trato que acreditara sin debitar, o que se escribiera a medias, crearía piedra o madera de la nada: la
forma más directa de hacer trampa en una economía entre jugadores, y la que más se nota cuando se repite.

**Cómo se garantiza.** `DB` — `TradeRepo.Accept` aplica los deltas de las dos ciudades y la transición de la oferta en
un solo `InTx`; cualquier error revierte todo. `DOMAIN` — los deltas de las dos ciudades se calculan de las mismas
líneas de la oferta; la reserva y su devolución recorren las mismas líneas (ADR-032 §2).

**Cómo se verifica.** Tests previstos: `Test_INV_TRADE_001_UnTratoConFalloEnUnDeltaNoMueveNada` (integration) y, en la
simulación, el golden `comercio` y la suma de los dos almacenes antes y después de cada trato.

**Violación en runtime.** No hay estado intermedio que detectar: la transacción es la garantía.

---

<a id="inv-trade-002"></a>
## INV-TRADE-002 — Toda oferta aceptada se aceptó con un tratado vigente entre los dos

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | E10 |
| Política ante violación | REJECT |
| Cobertura | **Sin cobertura**: registrado el 2026-11-04 con SPEC-TRADE, antes de escribir su código |

**Enunciado.** Una oferta pasa a `ACCEPTED` sólo si, en el tick en que se acepta, los dos jugadores tienen un tratado
`ACTIVE` de cualquier escalón (`TRADE`, `NON_AGGRESSION` o `ALLIANCE`) cuyo fin no ha llegado.

**Razón.** Sin esta propiedad el tratado de comercio no significaría nada, y romper un tratado no cortaría el comercio
al terminar su preaviso.

**Cómo se garantiza.** `DOMAIN` — la validación del tick comprueba `Treaty.AllowsTradeAt(now)` antes de reservar; la
fase 5 cierra las ofertas de un par sin tratado (`RN-TRADE-006`).

**Cómo se verifica.** Tests previstos: `TestAceptarSinTratadoSeRechaza` y `TestElFinDelPreavisoCierraLasOfertas`
(simulation).

**Violación en runtime.** La orden se rechaza con `TREATY_REQUIRED` y no se reserva nada.

---

<a id="inv-trade-003"></a>
## INV-TRADE-003 — Entre dos jugadores, como mucho una oferta pendiente

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E10 |
| Política ante violación | REJECT |
| Cobertura | **Sin cobertura**: registrado el 2026-11-04 con SPEC-TRADE, antes de escribir su código |

**Enunciado.** Para cada par de jugadores existe como mucho una fila de `trade_offers` con `status = 'PROPOSED'`, sea
quien sea el que la propuso.

**Razón.** Con dos ofertas abiertas entre los mismos jugadores, aceptar una no diría nada de la otra, y dos
aceptaciones seguidas podrían vaciar un almacén que el jugador creía comprometido sólo una vez.

**Cómo se garantiza.** `DB` — `trade_offers_one_pending_per_pair`, el índice único parcial sobre
`(least(proposer_id, target_id), greatest(proposer_id, target_id))` (migración `000013`). `DOMAIN` — `trade.propose` se
valida contra las ofertas pendientes de la RAM (`RN-TRADE-001`).

**Cómo se verifica.** Tests previstos: `Test_INV_TRADE_003_UnaOfertaPendientePorPar` (integration: el índice, en las
dos direcciones) y `TestRechazosDelComercio` (simulation).

**Violación en runtime.** La transacción falla por el índice y la orden se rechaza con `INVALID_TARGET`.

---

<a id="inv-trade-004"></a>
## INV-TRADE-004 — Una oferta sólo la cierra quien tiene derecho a hacerlo

| Campo | Valor |
|---|---|
| Severidad | CRITICO |
| Aplicación | DOMAIN, TEST |
| Milestone | E10 |
| Política ante violación | REJECT |
| Cobertura | **Sin cobertura**: registrado el 2026-11-04 con SPEC-TRADE, antes de escribir su código |

**Enunciado.** Una oferta pasa de `PROPOSED` a `ACCEPTED` o a `REJECTED` sólo por una orden de `target_id`, y a
`WITHDRAWN` sólo por una de `proposer_id`.

**Razón.** Si quien ofrece pudiera aceptar su propia oferta, vaciaría el almacén del otro a cambio de lo que él
quisiera darle.

**Cómo se garantiza.** `DOMAIN` — la validación del tick compara al autor de la orden con `proposer_id` y `target_id`
antes de reservar; la transacción condiciona la escritura al estado esperado.

**Cómo se verifica.** Tests previstos: `TestSoloRespondeElDestinatarioYSoloRetiraQuienOfrece` (`internal/domain/trade`)
y `TestRechazosDelComercio` (simulation).

**Violación en runtime.** La orden se rechaza con `INVALID_TARGET` y no se escribe nada.

---

## Trazabilidad

| Invariante | Componente propietario | Relacionado con |
|---|---|---|
| INV-TRADE-001 | `postgres.TradeRepo`, `internal/game/simulation` | [INV-ECON-001](economy.md#inv-econ-001), [INV-ECON-002](economy.md#inv-econ-002) |
| INV-TRADE-002 | `internal/game/simulation`, `internal/domain/diplomacy` | [INV-DIPLO-002](diplomacy.md#inv-diplo-002) |
| INV-TRADE-003 | `migrations/`, `internal/game/simulation` | [INV-DIPLO-002](diplomacy.md#inv-diplo-002) |
| INV-TRADE-004 | `internal/domain/trade`, `internal/game/simulation` | [INV-DIPLO-001](diplomacy.md#inv-diplo-001), [INV-SEC-004](security.md#inv-sec-004) |

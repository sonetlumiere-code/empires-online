# El comercio desde cualquier ciudad

Propuesta para que un jugador con varias ciudades **elija de cuál sale** un trato o una oferta del mercado, y de cuál
paga una compra: hoy todo sale de la capital y llega a ella. La ciudad elegida pone su almacén, su mercado y su
alcance de caravana ([trade-reach.md](trade-reach.md)); sin decir nada, sigue siendo la capital.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-TRADE-FROM-CITY` |
| Estado | Implemented (2026-11-25). Producto delegó en la recomendación del agente (sesión desatendida del 2026-11-25): las siete preguntas de §10, con su recomendación; en `main` desde la fusión `731656e`, con la CI en verde. |
| Milestone | E38 El comercio desde cualquier ciudad (trigésimo octavo slice después de M7) |
| Canon | §11 (comercio), §13 (protocolo) |
| Depende de | [trade.md](trade.md), [markets.md](markets.md), [caravans.md](caravans.md), [trade-reach.md](trade-reach.md), [multi-city.md](multi-city.md), [more-cities.md](more-cities.md), [websocket-protocol.md](websocket-protocol.md) |
| Decisiones | Sin ADR nuevo ni migración: `trade_offers.proposer_city_id`, `market_offers.city_id` y las caravanas ya guardan la ciudad; hoy se rellenan con la capital. |
| Reemplaza a | La Q7 de [multi-city.md](multi-city.md) («el comercio sale de la capital»), para las órdenes de §4.1. |
| Invariantes | No asigna invariantes. Regla nueva: `RN-TRADE-010` (§4.4). |

## 1. Objetivo

Desde E34 un jugador tiene dos ciudades y desde E37 hasta cuatro, pero tratos, ofertas y compras salen de la capital:
una segunda ciudad junto a otro jugador no puede comerciar con él, y su almacén sólo sirve para sí misma. La Q7 de
[multi-city.md](multi-city.md) lo dejó para un slice propio. Este lo abre con un campo opcional en tres órdenes.

### 1.1 Qué hace hoy el comercio (lectura del código)

- `trade.propose` toma la ciudad del que propone y la del destinatario con `State.CityOf` (capitales,
  `internal/game/simulation/trade.go`), comprueba el almacén y la ruta de caravana entre las dos y guarda las dos en
  `trade_offers`. Aceptar usa las ciudades guardadas.
- `market.post` y `market.buy` toman la capital (`market.go`), exigen un mercado completo en ella y cobran de su
  almacén; las caravanas de una compra van entre la ciudad del vendedor y la del comprador.
- El protocolo no lleva ciudad en esas tres órdenes; el HUD las manda desde la ciudad del panel sin decirlo.

## 2. Scope

**Entra.** `cityId` opcional en `trade.propose`, `market.post` y `market.buy` (§4.1); su validación (§4.2); el HUD,
que manda la ciudad del panel (§4.3); los tests y el humo.

**No entra.** Elegir la ciudad del que acepta un trato (Q2); que una caravana capturada vaya a otra ciudad que la
capital; cambiar los bots (Q6).

## 3. Actores

Jugador (elige la ciudad), Game Server (valida y guarda la ciudad), cliente (manda la del panel).

## 4. Modelo

### 4.1 Las órdenes (Q1, Q3)

| Orden | `cityId` (opcional) | Sin él |
|---|---|---|
| `trade.propose` | La ciudad propia de la que sale lo que da y a la que llega lo que pide. | La capital. |
| `market.post` | La ciudad propia que pone la custodia y recibe lo vendido; necesita su mercado. | La capital. |
| `market.buy` | La ciudad propia que paga y recibe lo comprado; necesita su mercado. | La capital. |

### 4.2 Qué se comprueba (Q4, Q5)

La ciudad tiene que ser del jugador y estar en pie (`CITY_NOT_FOUND` si no). Lo demás es lo de hoy con esa ciudad en
lugar de la capital: su almacén, su mercado completo, y la ruta de caravana desde ella (`RN-CARAVAN-002`, E30).

### 4.3 El HUD (Q7)

Las órdenes de comercio y mercado salen de la ciudad que muestra el panel (la de «H»); el panel lo dice («desde
<ciudad>») cuando no es la capital. Sin cambio, sigue mandando la capital.

### 4.4 Reglas

| ID | Regla |
|---|---|
| `RN-TRADE-010` | `trade.propose`, `market.post` y `market.buy` salen de la ciudad propia en pie que diga `cityId`, o de la capital sin él; esa ciudad pone el almacén, el mercado y el origen de la ruta. |

## 5. Errores

| Código | Cuándo |
|---|---|
| `CITY_NOT_FOUND` | `cityId` no es una ciudad propia en pie. |

El resto, los de [trade.md](trade.md) y [markets.md](markets.md), con la ciudad elegida.

## 6. Eventos y contratos de red

`cityId` (entero, opcional) en el payload de `trade.propose`, `market.post` y `market.buy`. Un cliente viejo no lo manda
y todo sigue saliendo de la capital.

## 7. Persistencia

Sin migración: la ciudad elegida va a las columnas que hoy se rellenan con la capital.

## 8. Invariantes

No asigna invariantes. Cita [INV-SEC-001](../invariants/security.md#inv-sec-001): la ciudad la valida el servidor.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| Simulación | Las tres órdenes con la segunda ciudad (su almacén, su mercado, su ruta), con una ajena o caída (`CITY_NOT_FOUND`) y sin `cityId` (la capital). |
| Contract | `cityId` opcional en Zod y en Go; un e2e de `trade.propose` con `cityId`. |
| Cliente (Vitest) | Las órdenes salen de la ciudad del panel. |
| Humo | `--multiciudad`: una oferta del mercado o un trato desde la segunda ciudad. |

### 9.1 Precisiones de la implementación (2026-11-25)

- **Precisión (2026-11-25): la ciudad del panel.** El HUD manda `cityId` cuando la ciudad del panel (la de «H») no es la
  capital (`commerceCityId` en `apps/web/src/state/multiCity.ts`). El panel del mercado ya muestra la ciudad del panel;
  el rótulo «desde <ciudad>» de §4.3 en el panel de comercio queda para otro slice: el que propone ya ve en el panel la
  ciudad que tiene seleccionada.
- **Precisión (2026-11-25): sin opción nueva del humo.** Comerciar desde la segunda pide un mercado en ella o un tratado
  con alguien a su alcance: la prueba en vivo repite `--mercado` y `--comercio` desde la capital (sin `cityId`), y el
  camino con `cityId` lo cubren los tests de simulación y el e2e de `trade.propose`.
- **Precisión (2026-11-26): la capital, la de id menor de todas.** `commerceCityId` tomaba por capital la primera
  ciudad propia a la vista: con la capital fuera del área, el panel mostraba otra y la orden salía sin `cityId`, de la
  capital. Ahora la capital es la de id menor entre las cargadas y las de `ownCities` del snapshot
  ([multi-city.md](multi-city.md) §9.1). Visto en el navegador: `smoke_runner`, con el panel en su cuarta ciudad (la 61,
  tomada con su mercado), publica en el tablón desde su almacén (la piedra baja de 200 a 134 y vuelve a 194 al retirar),
  el tablón mide la distancia desde ella, y su trato con `dev_nortepolis` queda con `proposer_city_id = 61`.

## 10. Decisiones de producto (2026-11-25)

Producto delegó en la recomendación del agente (sesión desatendida del 2026-11-25): cada fila deja la decisión (la
recomendación, en negrita) y las alternativas descartadas. Son decisiones de producto, no de ingeniería: cambiarlas es
cambiar la spec, y la persona usuaria puede revisarlas al volver.

| # | Pregunta | Decisión | Descartadas |
|---|---|---|---|
| Q1 | ¿Qué órdenes eligen ciudad? | **`trade.propose`, `market.post` y `market.buy`.** Son las que ponen almacén y ruta del que ordena. | **También las caravanas sueltas** (no hay orden de caravana sin trato). |
| Q2 | ¿Y el que acepta un trato? | **Desde su capital, como hoy**: la oferta ya guarda las dos ciudades al proponerse. | **Elegir al aceptar** (cambia una oferta ya escrita y su ruta). |
| Q3 | ¿Sin decir ciudad? | **La capital**: los clientes viejos y los bots siguen igual. | **Obligatorio** (rompe a un cliente viejo). |
| Q4 | ¿Qué pide la ciudad? | **Propia, en pie y, para el mercado, con mercado completo.** | **El mercado de cualquier ciudad propia** (un mercado haría comerciar a todas). |
| Q5 | ¿El alcance? | **La ruta desde la ciudad elegida** (E30). | **Desde la capital** (la segunda no ganaría nada). |
| Q6 | ¿Los bots? | **No cambian**: comercian desde la capital. | **Desde la más cercana a la pareja** (otra medida que su propio slice). |
| Q7 | ¿El HUD? | **La ciudad del panel**, con «desde <ciudad>» si no es la capital. | **Un selector en cada panel** (más cliente). |

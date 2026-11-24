# ADR-051: Un jugador puede tener dos ciudades en pie, con una capital que responde por él y un hueco único por ciudad en la base

Propósito: fijar cómo el multiciudad ([SPEC-MULTI-CITY](../specs/multi-city.md)) deja a un jugador tener una segunda
ciudad sin cambiar la respuesta de las cuarenta preguntas que la simulación hace hoy por «la ciudad del jugador», y cómo
la base garantiza el tope de dos y que nadie se quede sin ciudad.

| Campo | Valor |
|---|---|
| **Estado** | **Aceptado** |
| **Fecha** | **2026-11-24** |
| Ámbito | `internal/game/simulation` (`State.CityOf`, `State.CitiesOf`, `unit.found_city`, la presencia, el bono de territorio, la caída), `internal/persistence/postgres` (la fundación, la caída), `internal/game/founding` (el sitio en un tile dado), migración `000031` |
| Índice | [README.md](README.md) |
| Relacionados | [ADR-050](ADR-050-city-fall.md) (la caída en una transacción; «una ciudad en pie» como índice de la base, que éste afloja), [ADR-034](ADR-034-territory-claims.md) (el encargo en dos fases que la fundación copia), [ADR-029](ADR-029-population-over-limit-and-house-destruction.md) (población por encima del límite) |

---

## Contexto

Producto delegó el 2026-11-24 las once preguntas de la spec en la recomendación del agente (sesión desatendida): la
segunda ciudad se **funda** con un aldeano en su tile, desde Bronce y pagando 800 de madera y 400 de piedra; **dos**
como mucho; la capital es la de id menor en pie; presencia y protección son del jugador; el bono de territorios sólo lo
cobra la capital; una ciudad que cae con otra en pie se arrasa sin refundar.

Lo que condiciona la forma ([SPEC-MULTI-CITY](../specs/multi-city.md) §1.1): `State.cityByOwner` guarda una ciudad por
jugador y `CityOf` tiene 41 llamadas, casi todas preguntas por el jugador (civilización, bando, clan, protección del
atacante, destino del comercio); la base impide dos ciudades en pie con `cities_one_standing_per_owner`; la presencia
vive en columnas de la ciudad; el bono de territorio se cuenta por jugador.

## Decisión

### 1. La capital responde por el jugador

`State.CityOf(player)` devuelve la **capital**: la ciudad en pie de id menor. `AddCity` sólo la cambia si la nueva tiene
id menor (nunca, al fundar) y `RemoveCity` la pasa a la siguiente en pie. `State.CitiesOf(player)` devuelve todas, por
id. Las llamadas de hoy no cambian; cambian sólo las que son de cada ciudad: la presencia (todas), los centros de la
capital territorial (todos), el bono de territorio (sólo la capital), la caída (§3) y la vista.

### 2. El tope, en la base, con un hueco

`cities.slot smallint NOT NULL DEFAULT 1 CHECK (slot IN (1, 2))` y el índice único parcial `(owner_player_id, slot)
WHERE fallen_at IS NULL`, que sustituye a `cities_one_standing_per_owner` con el mismo nombre. Dos fundaciones a la vez
no pueden dar tres ciudades aunque se salten el lock: la segunda choca en el índice. La fundación toma el hueco libre;
la refundación, el de la caída. Subir el tope es ampliar el `CHECK`.

### 3. La caída elige en la transacción

`CityFallRepo` cuenta, bajo el lock de fundación, las ciudades en pie del vencido: con otra en pie, arrasa sin refundar
y reasigna lo suyo a la que queda (la capital tras la caída); si no, refunda como en ADR-050. La RAM aplica el resultado
en un solo comando: en ningún tick hay cero ciudades en pie ni tres.

### 4. La fundación es un encargo en dos fases

`unit.found_city` se valida en la simulación, reserva lo pagado y encola el trabajo `city.found` (claves: la ciudad que
paga y el jugador). La transacción vuelve a comprobar el tope y el sitio, cobra, crea la ciudad, pasa el aldeano, crea
los otros dos y escribe `CityFounded`. Al volver, la RAM incorpora la ciudad por el camino del alta
(`handleIntroducePlayer`) y descuenta; si falla, la reserva vuelve.

## Alternativas descartadas

- **Un `cityId` explícito en cada pregunta por el jugador** (comercio, mercado, reclamación): más general, pero toca
  protocolo, cliente y bots en cuarenta sitios para un tope de dos. Queda para cuando se elija de dónde sale el comercio.
- **Un trigger que cuente las ciudades en pie**: no es seguro bajo concurrencia sin lock, y el índice sobre el hueco sí.
- **Guardar la capital en una columna**: habría que mantenerla en la caída y en la fundación; deducirla (id menor) no se
  puede desincronizar.

## Consecuencias

- `INV-CITY-020` pasa de «exactamente una» a «al menos una»; el tope y la caída sin refundar son invariantes nuevos
  ([INV-CITY-022](../invariants/city.md#inv-city-022), [INV-CITY-023](../invariants/city.md#inv-city-023)).
- La capital no se elige ni se traslada; el comercio sale siempre de ella.
- El `down` de `000031` falla si algún jugador tiene dos ciudades en pie.

# Más ciudades

Propuesta para que el tope de ciudades en pie de un jugador deje de ser dos y **dependa de la era de su capital**: una
en Piedra, dos en Bronce, tres en Hierro y cuatro en Castillos. Fundar y tomar siguen las reglas de E34 y E36 con ese
tope; la capital, la caída y «H» ya funcionan con cualquier número.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-MORE-CITIES` |
| Estado | Draft (2026-11-25). Las preguntas de §10 esperan la decisión de producto. |
| Milestone | E37 Más ciudades (trigésimo séptimo slice después de M7) |
| Canon | §10 (eras y población), §12 (persistencia) |
| Depende de | [multi-city.md](multi-city.md), [city-taken.md](city-taken.md), [city-fall.md](city-fall.md), [bots-found.md](bots-found.md) |
| Decisiones | Sin ADR nuevo: [ADR-051](../decisions/ADR-051-multi-city-capital.md) §2 ya preveía subir el tope «ampliando el `CHECK`» del hueco. **Pide migración `000033`** (§7). |
| Reemplaza a | En [multi-city.md](multi-city.md), el tope fijo de dos (`RN-CITY-031`, Q2). |
| Invariantes | Precisa [INV-CITY-022](../invariants/city.md#inv-city-022) (§8). Reglas nuevas: `RN-CITY-040` y `RN-CITY-041` (§4.4). |

## 1. Objetivo

E34 abrió la segunda ciudad con un tope fijo de dos «lo más pequeño que ya es multiciudad» y la Q2 descartó el tope por
era porque pedía decidir qué pasa al bajar de era, que no existe. E35 midió que un constructor funda en cuanto llega a
Bronce y E36 que el vencedor con hueco se queda la ciudad. Con dos como techo, las eras de Hierro y Castillos no dan más
territorio: el que ya tiene dos no puede crecer más que subiendo edificios. Este slice hace que cada era abra un hueco.

### 1.1 Qué supone hoy el tope de dos (lectura del código)

- `city.MaxStandingPerOwner = 2` (`internal/domain/city/city.go`) lo usan el tick de la fundación (`found.go`), la toma
  (`assault.go`), la transacción de la fundación (`cityfound.go`) y la de la toma (`cityfall.go`), y la validación de
  carga (`loadcheck.go`).
- La base lo garantiza con `cities.slot` (`CHECK (slot IN (1, 2))`) y el índice por dueño y hueco (`000031`); la
  fundación y la toma eligen el hueco 2 si el 1 está ocupado.
- El cliente: `MAX_OWN_CITIES = 2` y «Ya tienes dos ciudades» en `foundCityBlockReason`
  (`apps/web/src/state/multiCity.ts`); «H» (`nextHome`) ya recorre cualquier número.
- Los bots: el constructor funda con menos de `MAX_OWN_CITIES` y juega una sola segunda ciudad (`secondCityId`).

## 2. Scope

**Entra.** El tope por era de la capital (§4.1); el hueco 1 a 4 en la base (§7); fundar y tomar con ese tope (§4.2);
el motivo del HUD; los bots que no pasan de dos (Q5); el humo `--multiciudad` con el tope nuevo.

**No entra.** Un coste de fundar que crezca (Q3); un selector de ciudades en el HUD (Q6); que los bots jueguen más de
una ciudad además de la capital; perder ciudades al bajar de era (no se baja).

## 3. Actores

Jugador (funda o toma), Game Server (valida el tope), PostgreSQL (el hueco), bots (no cambian su número).

## 4. Modelo

### 4.1 El tope (Q1, Q2)

| Era de la capital | Ciudades en pie como mucho |
|---|---|
| Piedra | 1 |
| Bronce | 2 |
| Hierro | 3 |
| Castillos | 4 |

La era que cuenta es la de la **capital** (la de id menor en pie, `RN-CITY-033`), la que responde por el jugador. El
techo absoluto es cuatro (`city.MaxStandingPerOwner`), y la base no deja más.

### 4.2 Fundar y tomar con el tope

- **Fundar** (`RN-CITY-030`): con menos ciudades en pie que el tope de la era de la capital. El coste no cambia (Q3).
- **Tomar** (`RN-CITY-036`): el vencedor tiene hueco si tiene menos que el tope de la era de su capital.
- **El hueco** de la ciudad nueva es el menor libre entre 1 y 4.

### 4.3 Si la capital cae (Q4)

La que queda de id menor pasa a capital; si está en una era más baja, el jugador puede quedarse con más ciudades que su
tope nuevo: no pierde ninguna, sólo no funda ni toma hasta bajar del tope o subir de era.

### 4.4 Reglas

| ID | Regla |
|---|---|
| `RN-CITY-040` | El tope de ciudades en pie de un jugador es el de la era de su capital: 1 en Piedra, 2 en Bronce, 3 en Hierro y 4 en Castillos; nunca más de cuatro. Precisa `RN-CITY-031`. |
| `RN-CITY-041` | Un jugador por encima de su tope (porque cayó su capital) conserva sus ciudades y no funda ni toma hasta estar por debajo. |

## 5. Errores

Sin códigos nuevos: el tope se rechaza con `INVALID_TARGET` («ya tienes N ciudades: el tope de tu capital es N»).

## 6. Eventos y contratos de red

Ninguno nuevo. El cliente deduce el tope de la era de su capital, que ya llega en `CityView`.

## 7. Persistencia

- **Migración `000033`:** `cities_slot_valid` pasa a `CHECK (slot BETWEEN 1 AND 4)`. El `down` vuelve a `(1, 2)` y falla
  si alguna ciudad está en un hueco 3 o 4.
- La fundación y la toma eligen el menor hueco libre bajo el cerrojo de fundación y cuentan contra el tope de la era de
  la capital, que leen en la misma transacción.

## 8. Invariantes

- **`INV-CITY-022`** (precisión): de «como mucho dos» a «como mucho el tope de la era de la capital al fundar o tomar, y
  nunca más de cuatro».

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| Unit (`internal/domain/city`) | El tope de cada era. |
| Simulación | Fundar la tercera en Hierro y no en Bronce; tomar con hueco por era; por encima del tope tras caer la capital. |
| Integración | La migración `000033`; la fundación en el hueco 3; el índice no deja un quinto. |
| Cliente (Vitest) | El motivo de «Fundar ciudad» con el tope por era; «H» con tres. |
| Bots (Vitest) | El constructor no funda una tercera. |
| Humo | `--multiciudad`: el rechazo dice el tope de la era. |

## 10. Preguntas de producto

Cada fila deja la recomendación del agente (en negrita) y las alternativas descartadas.

| # | Pregunta | Recomendación | Descartadas |
|---|---|---|---|
| Q1 | ¿De qué depende el tope? | **De la era: 1, 2, 3 y 4 de Piedra a Castillos.** Cada avance abre un hueco y se lee en el HUD sin otra regla. | **De los territorios conquistados** (otra cuenta que mantener); **fijo en tres** (Hierro y Castillos no dan nada). |
| Q2 | ¿La era de qué ciudad? | **La de la capital**, que responde por el jugador. | **La más alta de sus ciudades** (una ciudad avanzada sube el tope de todas); **la de la que funda** (dos ciudades, dos topes). |
| Q3 | ¿Cuesta lo mismo cada fundación? | **Sí, 800/400**, como E34: el tope ya limita. | **Un coste que crece con cada ciudad** (otra tabla que equilibrar sin datos). |
| Q4 | ¿Y si cae la capital y la nueva está en una era más baja? | **Conserva sus ciudades; sólo no funda ni toma más** (RN-CITY-041). | **Pierde las que sobran** (¿cuáles?, y sin aviso); **la capital pasa a ser la de era más alta** (cambia RN-CITY-033). |
| Q5 | ¿Y los bots? | **No pasan de dos**: el constructor funda una y juega una; medir más ciudades es otro slice. | **Fundan hasta el tope** (el decisor juega una sola segunda). |
| Q6 | ¿Cambia el HUD? | **Sólo el motivo de «Fundar ciudad»; «H» ya recorre todas.** | **Un selector de ciudades** (más cliente; cuando haya más de cuatro). |

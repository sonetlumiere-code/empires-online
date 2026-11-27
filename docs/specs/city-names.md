# El nombre de las ciudades

Propuesta para que las ciudades de un jugador se llamen con **su propia serie de nombres** (`<usuario>polis`,
`<usuario>polis II`, `III`, `IV`) y tomen **el primer nombre libre** entre sus ciudades en pie, en lugar de añadir un
numeral al nombre que tenga hoy la capital.

| Campo | Valor |
|---|---|
| Spec ID | `SPEC-CITY-NAMES` |
| Estado | Accepted (2026-11-26). La persona usuaria aceptó las seis recomendaciones de §10. |
| Milestone | E40 El nombre de las ciudades (cuadragésimo slice después de M7) |
| Canon | §12 (persistencia) |
| Depende de | [city.md](city.md), [multi-city.md](multi-city.md), [more-cities.md](more-cities.md), [city-fall.md](city-fall.md), [city-taken.md](city-taken.md) |
| Decisiones | Sin ADR nuevo: el nombre no cambia ningún contrato ni ningún dueño de estado. Sin migración (Q6). |
| Precisa | El nombre de `RN-CITY-032` ([multi-city.md](multi-city.md)) y la precisión de §9.1 de [more-cities.md](more-cities.md); el nombre de `RN-CITY-037` ([city-taken.md](city-taken.md)) y de `RN-CITY-027` ([city-fall.md](city-fall.md)). |
| Invariantes | Nuevo: [INV-CITY-025](../invariants/city.md#inv-city-025) (§8). Reglas nuevas: `RN-CITY-043` a `RN-CITY-046` (§4.5). |

## 1. Objetivo

El repaso en el navegador de E33 a E39 ([milestones.md](../roadmap/milestones.md), «Hallazgos para otra sesión») vio en
el mapa una ciudad llamada `bot_constructor_2_e35polis II II`. El nombre es lo único que distingue en pantalla dos
ciudades de un mismo jugador (el panel, «H», el registro y los avisos lo usan), así que repetirlo o encadenar numerales
confunde justo cuando el jugador tiene varias.

### 1.1 Qué hace hoy el código (lectura del código y de la base de desarrollo)

- La primera ciudad se llama `username + "polis"` (`internal/httpapi/founding.go`, `cmd/devseed`), sin otra validación
  (`RN-CITY-011`).
- **Fundar** la nombra `city.CityName(capital.Name, n)` con `n = ciudades en pie + 1` (`found.go`): el nombre de la
  capital **de hoy** más « II», « III» o « IV».
- **Tomar** conserva el nombre de la caída (`RN-CITY-037`) y **refundar**, el de la caída (`RN-CITY-027`).

De ahí salen tres defectos, los tres vistos o posibles:

1. **Numerales encadenados.** Si cae la capital y la «X II» pasa a capital, la siguiente fundación sale «X II II». En
   la base: la 45 (`bot_constructor_2_e35polis`) cayó, la 49 (`…polis II`) quedó de capital y fundó la 50
   (`…polis II II`).
2. **Numerales repetidos.** El numeral es un recuento, no un hueco: con X, X II y X III en pie, si cae X II y se funda
   otra, sale una segunda «X III».
3. **Nombres ajenos y repetidos al tomar.** La tomada conserva el nombre de la caída. En la base, `smoke_runner` tiene en
   pie tres ciudades llamadas `smoke_caidapolis` (51, 53 y 61), y el vencido tiene otra con ese nombre (65). Si un
   jugador funda desde una capital tomada, la nueva lleva el nombre del otro con « II»; y si su última ciudad en pie es
   una tomada y cae, refunda con el nombre ajeno.

## 2. Scope

**Entra.** El nombre de la ciudad que se funda (Q1, Q2), el de la que se toma (Q3) y el de la que se refunda (Q4); que
lo elija la transacción que ya elige el hueco (§7); el humo `--multiciudad`, que hoy espera «<capital> II».

**No entra.** Que el jugador elija o cambie el nombre (`Fuera de MVP`, [city.md](city.md) `RN-CITY-011`); la unicidad
global de nombres (`RN-CITY-011` la descarta); renombrar las ciudades que ya existen (Q5); cambios de protocolo o del
cliente: el nombre ya llega en `CityView` y el cliente no lo interpreta.

## 3. Actores

Jugador (funda, toma o refunda), Game Server (elige el nombre en la transacción), PostgreSQL (lo guarda). Los bots no
cambian: identifican sus ciudades por id.

## 4. Modelo

### 4.1 La serie de un jugador (Q1)

El **nombre base** de un jugador es el de su primera ciudad, `username + "polis"`. Su **serie** son cuatro nombres, uno
por hueco posible (`city.MaxStandingPerOwner`):

| Orden | Nombre |
|---|---|
| 1 | `<base>` |
| 2 | `<base> II` |
| 3 | `<base> III` |
| 4 | `<base> IV` |

El usuario sólo admite `[A-Za-z0-9_-]` (`RN-PLAYER-003`), así que un nombre generado sólo tiene un espacio, el del
numeral: quitar « II», « III» o « IV» del final da siempre la base, sin ambigüedad.

### 4.2 El primer nombre libre (Q2)

Un nombre de la serie está **libre** si ninguna ciudad en pie del dueño lo lleva. Al fundar, la ciudad nueva toma el
primero libre de la serie del fundador. Como fundar pide estar por debajo del tope (como mucho tres en pie), siempre
queda uno libre, aunque alguna de las que están en pie lleve un nombre ajeno.

| Ciudades en pie | Funda | Hoy | Con esta regla |
|---|---|---|---|
| `Xpolis` | la segunda | `Xpolis II` | `Xpolis II` |
| `Xpolis II` (cayó la capital) | otra | `Xpolis II II` | `Xpolis` |
| `Xpolis`, `Xpolis III` (cayó la II) | otra | `Xpolis III` (repetida) | `Xpolis II` |
| `Ypolis` (tomada; cayó todo lo suyo) | otra | `Ypolis II` | `Xpolis` |

### 4.3 La tomada (Q3)

Conserva el nombre de la caída, como hasta ahora, **salvo** que el vencedor ya tenga en pie una ciudad con ese nombre:
entonces toma el primer libre de la serie de **ese** nombre (su base es el nombre sin numeral). Con la toma de la 61
del ejemplo, `smoke_runner` tendría `smoke_caidapolis`, `smoke_caidapolis II` y `smoke_caidapolis III`. Tomar pide
hueco, así que también aquí queda siempre uno libre.

### 4.4 La refundada (Q4)

La refundada es la única ciudad en pie del vencido, así que lleva su base, `<usuario>polis`: el primer libre de su
serie. Hoy lleva el nombre de la caída, que puede ser `Xpolis III` o el de una ciudad tomada a otro.

### 4.5 Reglas

| ID | Regla |
|---|---|
| `RN-CITY-043` | La serie de nombres de un jugador es `<usuario>polis` y ese nombre con « II», « III» y « IV»; la de un nombre generado, la de su base sin numeral. |
| `RN-CITY-044` | La ciudad que funda un jugador lleva el primer nombre de su serie que no lleva ninguna de sus ciudades en pie. Precisa el nombre de `RN-CITY-032`. |
| `RN-CITY-045` | La tomada conserva el nombre de la caída si el vencedor no tiene en pie otra con ese nombre; si la tiene, lleva el primero libre de la serie de ese nombre. Precisa el nombre de `RN-CITY-037`. |
| `RN-CITY-046` | La refundada lleva el nombre base de su dueño. Precisa el nombre de `RN-CITY-027`. |

## 5. Errores

Ninguno nuevo: siempre hay un nombre libre cuando hay hueco (§4.2, §4.3). Si no lo hubiera, la transacción ya habría
rechazado por el tope o por falta de hueco.

## 6. Eventos y contratos de red

Ninguno nuevo. `cityName` de los eventos de fundación, caída y toma lleva el nombre elegido, como hoy.

## 7. Persistencia

- **El nombre lo elige la transacción**, no el tick: la fundación (`cityfound.go`), la toma y la refundación
  (`cityfall.go`) ya leen las ciudades en pie del dueño bajo el cerrojo de fundación para elegir el hueco
  (`city.FreeSlot`); la misma lectura trae sus nombres y la de `players.username`, y una función pura de
  `internal/domain/city` elige el nombre. Así una toma y una fundación del mismo jugador a la vez no eligen el mismo.
  El tick ya incorpora la ciudad que devuelve la transacción (`FoundResolved`, `FallResolved`), con su id, su hueco
  y, desde este slice, el nombre elegido.
- **La semilla de la refundación no cambia:** la espiral sigue partiendo del nombre de la ciudad caída
  (`refoundSeed`, [city-fall.md](city-fall.md) §9), que el tick conoce antes de la transacción; sólo cambia el nombre
  con que nace la refundada.
- **Sin migración** (Q6): no hay índice único sobre `(owner_player_id, name)`.
- `city.CityName` y `city.SecondCityName` desaparecen y dejan paso a la función de la serie (§9.1).

## 8. Invariantes

- **[INV-CITY-025](../invariants/city.md#inv-city-025):** dos ciudades en pie de un mismo dueño no llevan el mismo nombre, y ningún nombre
  generado lleva dos numerales. Lo garantizan la función de dominio y las tres transacciones (Q6); se verifica por
  mutación de la función y de la lectura de nombres en la transacción.

## 9. Tests esperados

| Nivel | Qué |
|---|---|
| Unit (`internal/domain/city`) | La serie y el primer libre: las cuatro filas de §4.2, la base de un nombre con numeral, una serie con nombres ajenos en pie. |
| Simulación | La fundación aplica a la RAM el nombre que devuelve la transacción. |
| Integración | Fundar tras caer la capital da `<base>` y no «X II II»; tras caer la II da `<base> II`; tomar una ciudad con un nombre que el vencedor ya tiene da `… II`; refundar desde una «X III» da `<base>`. |
| Humo | `--multiciudad`: la fundada lleva el primer nombre libre de la serie, no «<capital> II». |

### 9.1 Precisiones de la implementación (2026-11-26)

- **Precisión (2026-11-26): las funciones.** `city.BaseName(username)` es `username + "polis"`; `city.SeriesBase`
  quita **todos** los numerales del final, así que una «X II II» de antes de E40 tiene por base «X»;
  `city.FirstFreeName(base, enPie)` recorre la base, « II», « III» y « IV» y, con los cuatro ocupados (que el tope ya
  impide), devuelve la base; `city.FoundedName` y `city.TakenName` son la fundación y la toma.
- **Precisión (2026-11-26): el tick ya no propone nombre.** `cityfall.Founding` pierde su campo `Name`: lo elige
  `FoundCity` con los nombres de la misma lectura de las ciudades en pie (`FOR UPDATE`, bajo el cerrojo de fundación)
  y el `username` de `players`. `cityfall.Fall.Name` sigue siendo el nombre de la caída: el de la tomada si no se repite,
  el `cityName` de los eventos de la caída y la semilla de la refundación.
- **Precisión (2026-11-26): la serie de la tomada no mira la del vencedor.** Si el vencedor ya tiene una
  `smoke_caidapolis`, la tomada es `smoke_caidapolis II` aunque sea la primera ciudad de la serie de otro jugador; al
  fundar, en cambio, el vencedor sigue en la suya (`smoke_runnerpolis II`).
- **Precisión (2026-11-26): el humo.** `--multiciudad` calcula el nombre esperado con `nombreDeLaFundada` (con su test
  en `scripts/smoke.test.mjs`) a partir de las propias que ve antes de fundar.
- **Tests escritos.** `TestLaFundadaLlevaElPrimerNombreLibreDeSuSerie`, `TestLaBaseDeUnNombreQuitaSusNumerales` y
  `TestLaTomadaConservaSuNombreSalvoQueSeRepita` (unit); `TestUnAldeanoFundaLaSegundaCiudad` (simulación: la RAM toma
  el nombre de la transacción); `TestLaFundacionTrasCaerLaCapitalRecuperaElNombreBase`,
  `TestLaFundacionTrasCaerLaDelMedioTomaSuNombre`, `TestLaTomadaConUnNombreRepetidoTomaElSiguiente` y
  `TestLaRefundadaLlevaElNombreBase` (integración, `city_names_integration_test.go`).

## 10. Decisiones de producto (2026-11-26)

La persona usuaria aceptó las seis recomendaciones el 2026-11-26: cada fila deja la decisión, en negrita, y las
alternativas descartadas. Cambiarlas es cambiar la spec.

| # | Pregunta | Decisión | Descartadas |
|---|---|---|---|
| Q1 | ¿De qué nombre parte la ciudad que se funda? | **De la base del jugador, `<usuario>polis`.** No depende de qué ciudad sea hoy la capital ni de si es tomada, y no hay que interpretar nombres. | **Del nombre de la capital sin su numeral** (con una capital tomada, la serie es la de otro jugador); **del nombre de la capital tal cual** (lo de hoy: «X II II»). |
| Q2 | ¿Qué numeral lleva? | **El primero libre entre las ciudades en pie, y la base sin numeral cuenta como el primero.** Al caer la capital, la siguiente fundación recupera su nombre. | **El primero libre empezando en II** (la base queda para la primera ciudad, aunque ya no exista: con tres en pie y la capital caída, `Xpolis` no vuelve); **el siguiente al más alto que se haya usado nunca** (V, VI…: no repite ni con las caídas, pero crece sin techo y pide recordar las caídas); **el recuento** (lo de hoy: repite). |
| Q3 | ¿Y la ciudad tomada? | **Conserva su nombre salvo que el vencedor ya tenga en pie otra igual; entonces, el primero libre de la serie de ese nombre.** Se lee «tomé su ciudad» y no hay dos iguales en el mismo dueño. | **Conserva siempre su nombre** (lo de hoy: tres `smoke_caidapolis` en `smoke_runner`); **pasa a la serie del vencedor** (no se ve que es tomada, y cambia los avisos de E39, que la nombran). |
| Q4 | ¿Y la que se refunda? | **El nombre base del vencido.** Es su única ciudad: vuelve a empezar con su nombre, nunca con el de una tomada a otro. | **El de la caída** (lo de hoy, decidido en E31 Q6 cuando sólo había una ciudad: puede ser `Xpolis III` o un nombre ajeno). |
| Q5 | ¿Se renombran las ciudades que ya existen? | **No.** Sólo hay datos de desarrollo y bots; la regla vale para las ciudades nuevas. | **Una migración que las renombra** (más código y un `down` que no sabe el nombre de antes). |
| Q6 | ¿Lo garantiza también la base? | **No: sólo la función de dominio y las transacciones, con sus tests.** El nombre es cosmético; un índice único haría fallar una caída o una toma entera por un nombre repetido. | **Un índice único parcial sobre `(owner_player_id, name)` de las ciudades en pie** (migración nueva; protege de un fallo futuro a costa de rechazar la caída). |

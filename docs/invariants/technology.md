# Invariantes de Tecnología (INV-TECH-xxx)

Propiedades del árbol tecnológico: qué puede investigar una ciudad y cuándo, y cómo llegan los efectos de las
tecnologías a la simulación.

Formato y severidades: [README.md](README.md). Economía: [economy.md](economy.md). Jugadores y rasgos de
civilización: [player.md](player.md). Spec: [../specs/technologies.md](../specs/technologies.md). Decisión:
[ADR-046](../decisions/ADR-046-technologies.md).

---

<a id="inv-tech-001"></a>
## INV-TECH-001 — Una ciudad investiga como mucho una tecnología, nunca durante un avance de era, y sólo una a su alcance

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DB, DOMAIN, TEST |
| Milestone | E25 |
| Política ante violación | REJECT (escritura) · REPAIR (arranque) |
| Cobertura | **Cubierto** por `TestLosRechazosDeInvestigar`, `TestLaInvestigacionYElAvanceNoConviven`, `TestGoldenInvestigacion` y `TestLaBaseRechazaLaInvestigacion` (`internal/game/simulation`), `TestLaOrdenDeInvestigar` (`internal/domain/technology`), `TestLaCargaDescartaLasInvestigacionesMalFormadas` (`cmd/server`), y `TestLaInvestigacionEnLaBase`, `TestLosTriggersDeLaInvestigacion` y `TestUnTriggerRechazaElEncargoSinDebitar` (integration, `internal/persistence/postgres`). Verificado por mutación el 2026-11-18, cada una vista fallar: investigar sin mirar el avance y avanzar sin mirar la investigación tumban `TestLaInvestigacionYElAvanceNoConviven`; la orden sin la era o sin los requisitos, `TestLosRechazosDeInvestigar`; la carga sin la era, `TestLaCargaDescartaLasInvestigacionesMalFormadas`; y cada trigger desactivado con `DISABLE TRIGGER` en la base de tests (y vuelto a `tgenabled = 'O'`), su rama de `TestLosTriggersDeLaInvestigacion` |

**Enunciado.** Una ciudad tiene como mucho una fila de `city_research`, y nunca a la vez que una de
`city_era_advances`. La tecnología de esa fila es de la era de la ciudad o de una anterior, común o exclusiva de la
civilización de su dueño, que el dueño no tiene todavía y cuyos requisitos tiene. Una fila de `player_technologies`
es de una tecnología común o de una exclusiva de la civilización del jugador. Lo mismo en la RAM del Game Server.

**Razón.** La investigación compite con el avance de era por la progresión de la ciudad (SPEC-TECHNOLOGIES Q8), y
la exclusividad de una tecnología por civilización es lo que impide que un jugador complete el árbol (pilar 4,
Q3). Una investigación de una era futura, sin sus requisitos o de otra civilización daría un efecto que el jugador no
podía tener.

**Cómo se garantiza.** `DOMAIN` — `technology.CanResearch` en el tick, antes de reservar el coste, y el avance de era
rechaza una ciudad que investiga. `DB` — la clave primaria de `city_research`, el trigger `city_research_guard`, el
trigger `city_era_advances_research_guard` y el trigger `player_technologies_civ_guard`, que rechazan con `23514`
([ADR-046](../decisions/ADR-046-technologies.md) §4).

**Cómo se verifica.** En la simulación, cada rechazo de `city.research` y la investigación y el avance que no
conviven en los dos órdenes. En la integración, la clave primaria y cada rama de los tres triggers.

**Violación en runtime.** Rechazo: la orden no escribe nada y vuelve con su código. Una investigación que la carga
encuentra mal formada se descarta en la RAM y se registra.

---

<a id="inv-tech-002"></a>
## INV-TECH-002 — Todo efecto de una tecnología es un dato del catálogo que se compone con la civilización en un único punto

| Campo | Valor |
|---|---|
| Severidad | ALTO |
| Aplicación | DOMAIN, TEST |
| Milestone | E25 |
| Política ante violación | FAIL_FAST (catálogo) |
| Cobertura | **Cubierto** por `TestLaComposicionDeLosEfectos` y `TestLaValidacionDelCatalogo` (`internal/domain/technology`), `TestGoldenInvestigacion`, `TestLaRuedaAcortaLaPolilinea`, `TestGremiosAcortaLaObra`, `TestLasHerramientasDePiedraAcortanElTrabajo`, `TestLaCesteriaCargaDoce`, `TestLaAlbanileriaDaCasasDe180`, `TestLasIncursionesCarganVeinticuatro`, `TestLasArmasDeBronceGolpeanNueve` y `TestLasInvestigacionesTrasUnReinicio` (`internal/game/simulation`), y `TestElCatalogoDeTecnologiasEnLaBase` (integration). Verificado por mutación el 2026-11-18, cada una vista fallar: `State.trait` sin las tecnologías tumba el golden y los ocho tests de los puntos de consumo; el catálogo que admite una clave desconocida, `TestLaValidacionDelCatalogo`; sumar los efectos en vez de componerlos, `TestLaComposicionDeLosEfectos` |

**Enunciado.** Todo efecto del catálogo `technologies` es una clave del catálogo de rasgos con un entero positivo por
mil. Todo valor que una tecnología cambia sale de `State.trait`: el rasgo de la civilización del jugador compuesto
con el efecto de cada tecnología que tiene y que lleva esa clave, en orden de código, con
`(r × e + 500) / 1000` y un mínimo de 1 en cada paso, aplicado en el punto de consumo de la clave al dar la orden.
Ningún sistema pregunta por el código de una tecnología.

**Razón.** El roadmap exige que los efectos sean datos y no condicionales en el dominio, y las golden de la simulación
necesitan que el mismo catálogo dé siempre el mismo número (canon §1.5). Componer en un solo sitio evita que un punto
de consumo olvide una tecnología o la aplique dos veces.

**Cómo se garantiza.** `DOMAIN` — `technology.Catalog` valida el catálogo al cargar y el arranque falla si no es
válido; `State.trait` es la única vía para leer un rasgo, y su valor compuesto se guarda por jugador al cargar y al
terminar cada investigación ([ADR-046](../decisions/ADR-046-technologies.md) §1 y §2).

**Cómo se verifica.** En el dominio, la composición y la validación del catálogo. En la simulación, cada uno de los
siete puntos de consumo con una tecnología y con una tecnología compuesta con el rasgo, y la recuperación de los
valores compuestos tras un reinicio.

**Violación en runtime.** Un catálogo inválido impide arrancar.

---

## Trazabilidad

| Invariante | Componente propietario | Relacionado con |
|---|---|---|
| INV-TECH-001 | `internal/game/simulation`, `internal/persistence/postgres`, `internal/game/loadcheck` | [INV-CITY-018](city.md#inv-city-018), [INV-ECON-002](economy.md#inv-econ-002) |
| INV-TECH-002 | `internal/domain/technology`, `internal/game/simulation` | [INV-PLAYER-012](player.md#inv-player-012) |

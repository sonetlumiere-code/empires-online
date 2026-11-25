package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/building"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/cityfall"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// La caída de una ciudad (SPEC-CITY-FALL, ADR-050). La simulación decide; este
// repositorio escribe cada asalto con su fila y la caída con la refundación en
// UNA transacción: nunca hay un instante con el jugador sin ciudad en pie ni
// con dos (INV-CITY-020).

// CityFallRepo escribe y lee los asaltos y las caídas.
type CityFallRepo struct {
	store       *Store
	cities      *CityRepo
	units       *UnitRepo
	territories *TerritoryRepo
}

// NewCityFallRepo crea el repositorio.
func NewCityFallRepo(s *Store, c *CityRepo, u *UnitRepo, t *TerritoryRepo) *CityFallRepo {
	return &CityFallRepo{store: s, cities: c, units: u, territories: t}
}

// AssaultStart es el comienzo de un asalto.
type AssaultStart struct {
	Assault cityfall.Assault
	// Owner es el dueño de la ciudad asaltada, a quien se le cuenta.
	Owner uuid.UUID
	Tick  uint64
}

// AssaultEnd es el fin sin caída de un asalto.
type AssaultEnd struct {
	AssaultID int64
	Reason    string
}

// StartAssault escribe el asalto ACTIVE y CityAssaultStarted al asaltado
// (ADR-050 §2). Un reintento de la misma petición devuelve la fila que ya
// escribió. Otro asalto activo sobre la ciudad o con la unidad
// (INV-CITY-021), o una ciudad que ya cayó, es ErrConflict.
func (r *CityFallRepo) StartAssault(ctx context.Context, s AssaultStart) (int64, error) {
	var id int64
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		a := s.Assault
		var caida bool
		switch err := tx.QueryRow(ctx, `SELECT fallen_at IS NOT NULL FROM cities WHERE id = $1`, a.CityID).Scan(&caida); {
		case errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("%w: la ciudad %d no existe", ErrConflict, a.CityID)
		case err != nil:
			return fmt.Errorf("leer la ciudad asaltada %d: %w", a.CityID, normalize(err))
		case caida:
			return fmt.Errorf("%w: la ciudad %d ya cayó", ErrConflict, a.CityID)
		}
		err := tx.QueryRow(ctx,
			`INSERT INTO city_assaults
			   (city_id, unit_id, player_id, from_city_id, x, y, started_at_ms, ends_at_ms, status, request_id)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'ACTIVE', $9)
			 ON CONFLICT (player_id, request_id) DO NOTHING
			 RETURNING id`,
			a.CityID, a.UnitID, a.PlayerID, a.FromCityID, a.X, a.Y, a.StartedAtMs, a.EndsAtMs, a.RequestID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			// Ya estaba escrita: un reintento de la cola.
			return tx.QueryRow(ctx,
				`SELECT id FROM city_assaults WHERE player_id = $1 AND request_id = $2`,
				a.PlayerID, a.RequestID).Scan(&id)
		}
		if err != nil {
			if IsUniqueViolation(err) {
				return fmt.Errorf("%w: la ciudad %d o la unidad %d ya asaltan", ErrConflict, a.CityID, a.UnitID)
			}
			return fmt.Errorf("escribir el asalto de la ciudad %d: %w", a.CityID, normalize(err))
		}
		owner := s.Owner
		return appendEvent(ctx, tx, worldEvent{
			EventType: eventlog.EventCityAssaultStarted, Tick: s.Tick, PlayerID: &owner,
			CounterpartID: counterpart(a.PlayerID),
			Payload: map[string]any{
				"assaultId": id, "cityId": a.CityID, "unitId": a.UnitID, "x": a.X, "y": a.Y,
				"atMs": a.StartedAtMs, "fallsAtMs": a.EndsAtMs,
			},
		})
	})
	return id, err
}

// EndAssault cierra un asalto sin caída. Idempotente: uno ya cerrado no cambia.
func (r *CityFallRepo) EndAssault(ctx context.Context, e AssaultEnd) error {
	if _, err := r.store.pool.Exec(ctx,
		`UPDATE city_assaults SET status = 'BROKEN', end_reason = $2, ended_at = now()
		  WHERE id = $1 AND status = 'ACTIVE'`, e.AssaultID, e.Reason); err != nil {
		return fmt.Errorf("cerrar el asalto %d: %w", e.AssaultID, normalize(err))
	}
	return nil
}

// ListActiveAssaults devuelve los asaltos activos, por ciudad, para la carga al
// arrancar (ADR-050 §2).
func (r *CityFallRepo) ListActiveAssaults(ctx context.Context) ([]cityfall.Assault, error) {
	rows, err := r.store.pool.Query(ctx,
		`SELECT id, city_id, unit_id, player_id, from_city_id, x, y, started_at_ms, ends_at_ms, request_id
		   FROM city_assaults WHERE status = 'ACTIVE' ORDER BY city_id`)
	if err != nil {
		return nil, fmt.Errorf("leer los asaltos activos: %w", normalize(err))
	}
	defer rows.Close()
	var out []cityfall.Assault
	for rows.Next() {
		var a cityfall.Assault
		if err := rows.Scan(&a.ID, &a.CityID, &a.UnitID, &a.PlayerID, &a.FromCityID, &a.X, &a.Y,
			&a.StartedAtMs, &a.EndsAtMs, &a.RequestID); err != nil {
			return nil, fmt.Errorf("leer un asalto: %w", normalize(err))
		}
		out = append(out, a)
	}
	return out, normalize(rows.Err())
}

// FallCity es la transacción de ADR-050 §3: arrasa la ciudad, refunda a su
// dueño y resuelve lo que colgaba de ella (SPEC-CITY-FALL §4.2, §4.3). El sitio
// se busca fuera de la transacción y se vuelve a comprobar dentro, bajo el
// cerrojo de fundación, como en el alta; si otra fundación se adelantó, se
// repite la búsqueda. Un asalto que ya no está ACTIVE o una ciudad que ya cayó
// son ErrConflict: no se escribe nada.
func (r *CityFallRepo) FallCity(ctx context.Context, f cityfall.Fall) (*cityfall.FallResult, error) {
	if f.Place == nil {
		return nil, fmt.Errorf("la caída necesita con qué buscar el sitio de la ciudad nueva")
	}
	if f.Now.IsZero() {
		return nil, fmt.Errorf("la caída necesita el instante del reloj inyectado")
	}
	def, err := unit.Lookup(unit.TypeVillager)
	if err != nil {
		return nil, err
	}
	// SPEC-MULTI-CITY RN-CITY-035: con otra ciudad en pie, la caída no refunda.
	// Si esa otra cayó mientras tanto, la transacción lo ve y se refunda.
	if kept, err := r.otherStanding(ctx, r.store.pool, f.OwnerID, f.CityID); err != nil {
		return nil, err
	} else if kept != 0 {
		result, err := r.fall(ctx, f, cityfall.Site{}, def, kept)
		if !errors.Is(err, errKeptGone) {
			return result, err
		}
	}
	for intento := 0; intento < maxPlacementAttempts; intento++ {
		existing, err := r.standingCentersBut(ctx, r.store.pool, f.CityID)
		if err != nil {
			return nil, err
		}
		site, err := f.Place(existing)
		if err != nil {
			return nil, err
		}
		if len(site.Spawns) != player.InitialVillagers {
			return nil, errAldeanos(len(site.Spawns))
		}
		result, err := r.fall(ctx, f, site, def, 0)
		if !errors.Is(err, errPlacementStale) {
			return result, err
		}
	}
	return nil, ErrPlacementContended
}

// errKeptGone: la otra ciudad en pie del vencido cayó antes de confirmar.
var errKeptGone = errors.New("la otra ciudad del vencido ya no está en pie")

// otherStanding es la otra ciudad en pie de un jugador, la de id menor, o 0.
func (r *CityFallRepo) otherStanding(ctx context.Context, db DB, owner uuid.UUID, except int64) (int64, error) {
	var id int64
	err := db.QueryRow(ctx,
		`SELECT id FROM cities WHERE owner_player_id = $1 AND fallen_at IS NULL AND id <> $2 ORDER BY id LIMIT 1`,
		owner, except).Scan(&id)
	if errors.Is(normalize(err), ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("leer la otra ciudad del jugador: %w", normalize(err))
	}
	return id, nil
}

// standingCentersBut son los centros de las ciudades en pie salvo una, en
// orden de id.
func (r *CityFallRepo) standingCentersBut(ctx context.Context, db DB, except int64) ([]world.Tile, error) {
	rows, err := db.Query(ctx,
		`SELECT center_x, center_y FROM cities WHERE fallen_at IS NULL AND id <> $1 ORDER BY id`, except)
	if err != nil {
		return nil, fmt.Errorf("leer los centros de las ciudades en pie: %w", normalize(err))
	}
	defer rows.Close()
	var out []world.Tile
	for rows.Next() {
		var t world.Tile
		if err := rows.Scan(&t.X, &t.Y); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, normalize(rows.Err())
}

// fall es la transacción de la caída. Con kept distinto de 0, la otra ciudad en
// pie del vencido recibe lo de la caída y no se refunda (RN-CITY-035); si ya no
// está en pie, errKeptGone y no se escribe nada.
func (r *CityFallRepo) fall(ctx context.Context, f cityfall.Fall, site cityfall.Site, def unit.Definition,
	kept int64) (*cityfall.FallResult, error) {
	res := &cityfall.FallResult{}
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, foundingLockKey); err != nil {
			return fmt.Errorf("serializar la refundación: %w", err)
		}
		// La refundación es un recuento, no una creación por encima del límite:
		// los supervivientes pueden dejar la ciudad nueva por encima (ADR-029 §3,
		// SPEC-CITY-FALL Q4).
		if _, err := tx.Exec(ctx, `SET LOCAL eo.population_recount = 'on'`); err != nil {
			return fmt.Errorf("declarar el recuento de población: %w", err)
		}

		// 1. La ciudad sigue en pie y el asalto activo.
		var (
			caida       bool
			owner       uuid.UUID
			presence    string
			lastOffline *time.Time
		)
		if err := tx.QueryRow(ctx,
			`SELECT fallen_at IS NOT NULL, owner_player_id, presence_state, last_offline_at
			   FROM cities WHERE id = $1 FOR UPDATE`, f.CityID).
			Scan(&caida, &owner, &presence, &lastOffline); err != nil {
			return fmt.Errorf("leer la ciudad caída %d: %w", f.CityID, normalize(err))
		}
		if caida || owner != f.OwnerID {
			return fmt.Errorf("%w: la ciudad %d ya cayó", ErrConflict, f.CityID)
		}
		var estado string
		if err := tx.QueryRow(ctx, `SELECT status FROM city_assaults WHERE id = $1 FOR UPDATE`, f.AssaultID).
			Scan(&estado); err != nil {
			return fmt.Errorf("leer el asalto %d: %w", f.AssaultID, normalize(err))
		}
		if estado != "ACTIVE" {
			return fmt.Errorf("%w: el asalto %d ya no está activo", ErrConflict, f.AssaultID)
		}
		if kept != 0 {
			var sigue bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM cities WHERE id = $1 AND owner_player_id = $2 AND fallen_at IS NULL)`,
				kept, f.OwnerID).Scan(&sigue); err != nil {
				return fmt.Errorf("leer la otra ciudad %d: %w", kept, normalize(err))
			}
			if !sigue {
				return errKeptGone
			}
		}
		// SPEC-CITY-TAKEN RN-CITY-036: con hueco, el vencedor se la queda.
		take, err := r.takeSlot(ctx, tx, f)
		if err != nil {
			return err
		}
		if f.Verify != nil && kept == 0 {
			existing, err := r.standingCentersBut(ctx, tx, f.CityID)
			if err != nil {
				return err
			}
			if !f.Verify(site.Center, existing) {
				return errPlacementStale
			}
		}

		// 2. Arrasar: la ciudad cae, sus edificios caen con ella y sus encargos
		// se pierden con lo pagado (RN-CITY-028). El almacén arde (Q5).
		if _, err := tx.Exec(ctx,
			`UPDATE cities SET fallen_at = $2, population = 0, version = version + 1 WHERE id = $1`,
			f.CityID, f.Now.UTC()); err != nil {
			return fmt.Errorf("arrasar la ciudad %d: %w", f.CityID, normalize(err))
		}
		if take != nil {
			if err := r.takeCity(ctx, tx, f, take, res); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE buildings SET status = 'DESTROYED' WHERE city_id = $1 AND status <> 'DESTROYED'`, f.CityID); err != nil {
			return fmt.Errorf("derribar los edificios de la ciudad %d: %w", f.CityID, normalize(err))
		}
		for _, tabla := range []string{"city_productions", "city_era_advances", "city_crafts", "city_research"} {
			if _, err := tx.Exec(ctx, `DELETE FROM `+tabla+` WHERE city_id = $1`, f.CityID); err != nil {
				return fmt.Errorf("borrar los encargos de %s de la ciudad %d: %w", tabla, f.CityID, normalize(err))
			}
		}
		if _, err := tx.Exec(ctx,
			`UPDATE city_stocks SET amount = 0, version = version + 1 WHERE city_id = $1 AND amount <> 0`, f.CityID); err != nil {
			return fmt.Errorf("vaciar el almacén de la ciudad %d: %w", f.CityID, normalize(err))
		}
		if _, err := tx.Exec(ctx,
			`UPDATE city_assaults SET status = 'FELL', ended_at = now() WHERE id = $1`, f.AssaultID); err != nil {
			return fmt.Errorf("cerrar el asalto %d: %w", f.AssaultID, normalize(err))
		}

		// 3. Su territorio capital queda libre (§4.2).
		freed, err := freeCapital(ctx, tx, f.OwnerID, f.Center)
		if err != nil {
			return err
		}
		res.Freed = freed
		if take != nil {
			if err := r.settleTaken(ctx, tx, f, res); err != nil {
				return err
			}
		}

		if kept != 0 {
			if err := r.intoKept(ctx, tx, f, kept, res); err != nil {
				return err
			}
			return r.afterFall(ctx, tx, f, kept, res)
		}

		// 4. La ciudad nueva: el nombre y la era de la caída, el almacén a cero y
		// tres aldeanos, como un alta (RN-CITY-027).
		populationCap, err := r.cities.PopulationCapOf(ctx, tx, f.Era)
		if err != nil {
			return err
		}
		fundada := f.Now
		c := &city.City{
			OwnerPlayerID: f.OwnerID, Name: f.Name, CenterX: site.Center.X, CenterY: site.Center.Y, Era: f.Era,
			PopulationLimit: populationCap, PresenceState: city.PresenceState(presence), LastOnlineAt: &fundada,
		}
		if _, err := r.cities.Create(ctx, tx, c); err != nil {
			return err
		}
		if lastOffline != nil {
			if _, err := tx.Exec(ctx, `UPDATE cities SET last_offline_at = $2 WHERE id = $1`, c.ID, *lastOffline); err != nil {
				return fmt.Errorf("copiar la desconexión a la ciudad %d: %w", c.ID, normalize(err))
			}
			c.LastOfflineAt = lastOffline
		}
		if err := CreateCityStocks(ctx, tx, c.ID); err != nil {
			return err
		}
		for _, spawn := range site.Spawns {
			u := &unit.Unit{
				PlayerID: f.OwnerID, CityID: &c.ID, Type: unit.TypeVillager, X: spawn.X, Y: spawn.Y,
				HP: def.MaxHP, MaxHP: def.MaxHP, Status: unit.StatusIdle,
			}
			if _, err := r.units.Create(ctx, tx, u); err != nil {
				return err
			}
			res.Villagers = append(res.Villagers, u)
		}
		// Las unidades vivas de la caída pasan a la nueva (RN-CITY-028).
		if _, err := tx.Exec(ctx,
			`UPDATE units SET city_id = $2 WHERE city_id = $1 AND status <> 'DEAD'`, f.CityID, c.ID); err != nil {
			return fmt.Errorf("pasar las unidades a la ciudad %d: %w", c.ID, normalize(err))
		}

		// 5. El territorio de la ciudad nueva, si estaba libre (RN-TERR-007).
		if site.TerritoryID != 0 {
			actual, err := r.territories.GetControl(ctx, tx, site.TerritoryID)
			if err != nil {
				return fmt.Errorf("leer el control del territorio %d: %w", site.TerritoryID, err)
			}
			if actual.OwnerType == territory.OwnerNone {
				ctrl, err := r.territories.ClaimForPlayer(ctx, tx, site.TerritoryID, f.OwnerID, f.Now.UTC(), actual.Version)
				if err != nil {
					return err
				}
				res.Claimed = &ctrl
			}
		}

		// 6. Población y límite de la nueva, como los recuenta la carga: las
		// unidades vivas sin las caravanas, y la era más cinco por territorio
		// conquistado (INV-CITY-017, INV-ECON-004).
		var conquistados int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM territory_control tc JOIN territories t ON t.id = tc.territory_id
			  WHERE tc.owner_type = 'PLAYER' AND tc.owner_id = $1
			    AND NOT ($2 BETWEEN t.min_x AND t.max_x AND $3 BETWEEN t.min_y AND t.max_y)`,
			f.OwnerID.String(), c.CenterX, c.CenterY).Scan(&conquistados); err != nil {
			return fmt.Errorf("contar los territorios conquistados: %w", normalize(err))
		}
		c.PopulationLimit = populationCap + territory.ConqueredBonus(conquistados)
		if err := tx.QueryRow(ctx,
			`UPDATE cities c SET population_limit = $2,
			        population = (SELECT count(*) FROM units u
			                       WHERE u.city_id = c.id AND u.status <> 'DEAD' AND u.unit_type <> 'CARAVAN'),
			        version = version + 1
			  WHERE c.id = $1 RETURNING population, version`, c.ID, c.PopulationLimit).
			Scan(&c.Population, &c.Version); err != nil {
			return fmt.Errorf("recontar la ciudad %d: %w", c.ID, normalize(err))
		}

		res.City = c
		return r.afterFall(ctx, tx, f, c.ID, res)
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

// freeCapital deja sin dueño el territorio que contiene el centro de la ciudad
// caída si era de su dueño, y devuelve su control; nil si no lo era.
func freeCapital(ctx context.Context, tx pgx.Tx, owner uuid.UUID, center world.Tile) (*territory.Control, error) {
	row := tx.QueryRow(ctx,
		`UPDATE territory_control tc
		    SET owner_type = 'NONE', owner_id = NULL, captured_at = NULL, version = tc.version + 1
		   FROM territories t
		  WHERE t.id = tc.territory_id AND tc.owner_type = 'PLAYER' AND tc.owner_id = $1
		    AND $2 BETWEEN t.min_x AND t.max_x AND $3 BETWEEN t.min_y AND t.max_y
		RETURNING tc.territory_id, tc.owner_type, tc.owner_id, tc.control_points, tc.contested, tc.captured_at, tc.version`,
		owner.String(), center.X, center.Y)
	c, err := scanControl(row)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("liberar el territorio capital: %w", err)
	}
	return &c, nil
}

// ids ejecuta una escritura con RETURNING id y devuelve los ids, en orden.
func ids(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]int64, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, normalize(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, normalize(err)
	}
	sortInt64(out)
	return out, nil
}

func sortInt64(v []int64) {
	for i := 1; i < len(v); i++ {
		for j := i; j > 0 && v[j] < v[j-1]; j-- {
			v[j], v[j-1] = v[j-1], v[j]
		}
	}
}

// afterFall resuelve lo que colgaba de la caída hacia target (la refundada o la
// otra ciudad en pie) y escribe el evento (RN-CITY-028, RN-CITY-035).
func (r *CityFallRepo) afterFall(ctx context.Context, tx pgx.Tx, f cityfall.Fall, target int64, res *cityfall.FallResult) error {
	var err error
	var nx, ny int32
	if err := tx.QueryRow(ctx, `SELECT center_x, center_y FROM cities WHERE id = $1`, target).Scan(&nx, &ny); err != nil {
		return fmt.Errorf("leer el centro de la ciudad %d: %w", target, normalize(err))
	}
	// 7. Lo que colgaba de la caída (RN-CITY-028).
	if res.ExpiredTrades, err = ids(ctx, tx,
		`UPDATE trade_offers SET status = 'EXPIRED', closed_at = $2
		  WHERE status = 'PROPOSED' AND (proposer_city_id = $1 OR target_city_id = $1)
		 RETURNING id`, f.CityID, f.Now.UTC()); err != nil {
		return fmt.Errorf("caducar los tratos de la ciudad %d: %w", f.CityID, err)
	}
	if res.WithdrawnMarket, err = ids(ctx, tx,
		`UPDATE market_offers SET status = 'WITHDRAWN', closed_at = $2
		  WHERE status = 'OPEN' AND city_id = $1
		 RETURNING id`, f.CityID, f.Now.UTC()); err != nil {
		return fmt.Errorf("retirar las ofertas de la ciudad %d: %w", f.CityID, err)
	}
	if res.RedirectedCaravans, err = ids(ctx, tx,
		`UPDATE caravans SET to_city_id = $2
		  WHERE status = 'IN_TRANSIT' AND to_city_id = $1
		 RETURNING id`, f.CityID, target); err != nil {
		return fmt.Errorf("redirigir las caravanas de la ciudad %d: %w", f.CityID, err)
	}

	// 8. El evento, a los dos (SPEC-CITY-FALL §6).
	payload := map[string]any{
		"assaultId": f.AssaultID, "cityId": f.CityID, "newCityId": target, "cityName": f.Name,
		"x": f.Center.X, "y": f.Center.Y, "newX": nx, "newY": ny, "atMs": f.AtMs,
	}
	if f.War != nil {
		payload["war"] = warPointsPayload(f.War)
	}
	if res.Taken != nil {
		payload["takenCityId"] = res.Taken.ID
	}
	vencido := f.OwnerID
	if err := appendEvent(ctx, tx, worldEvent{
		EventType: eventlog.EventCityFell, Tick: f.Tick, PlayerID: &vencido,
		CounterpartID: counterpart(f.VictorID), Payload: payload,
	}); err != nil {
		return err
	}
	return nil
}

// intoKept pasa a la otra ciudad en pie del vencido sus unidades vivas y la
// recuenta; si pasa a ser la capital, cobra el bono de territorio
// (SPEC-MULTI-CITY RN-CITY-034, RN-CITY-035).
func (r *CityFallRepo) intoKept(ctx context.Context, tx pgx.Tx, f cityfall.Fall, kept int64, res *cityfall.FallResult) error {
	if _, err := tx.Exec(ctx,
		`UPDATE units SET city_id = $2 WHERE city_id = $1 AND status <> 'DEAD'`, f.CityID, kept); err != nil {
		return fmt.Errorf("pasar las unidades a la ciudad %d: %w", kept, normalize(err))
	}
	var bono int32
	if f.CityID < kept {
		// La caída era la capital: el bono pasa a la que queda, sin contar ninguna
		// capital territorial (la de la caída ya está libre).
		var conquistados int
		if err := tx.QueryRow(ctx,
			`SELECT count(*) FROM territory_control tc JOIN territories t ON t.id = tc.territory_id
			  WHERE tc.owner_type = 'PLAYER' AND tc.owner_id = $1
			    AND NOT EXISTS (SELECT 1 FROM cities c WHERE c.owner_player_id = $2 AND c.fallen_at IS NULL
			                     AND c.center_x BETWEEN t.min_x AND t.max_x AND c.center_y BETWEEN t.min_y AND t.max_y)`,
			f.OwnerID.String(), f.OwnerID).Scan(&conquistados); err != nil {
			return fmt.Errorf("contar los territorios conquistados: %w", normalize(err))
		}
		bono = territory.ConqueredBonus(conquistados)
	}
	if err := tx.QueryRow(ctx,
		`UPDATE cities c SET population_limit = population_limit + $2,
		        population = (SELECT count(*) FROM units u
		                       WHERE u.city_id = c.id AND u.status <> 'DEAD' AND u.unit_type <> 'CARAVAN'),
		        version = version + 1
		  WHERE c.id = $1 RETURNING population, population_limit`, kept, bono).
		Scan(&res.KeptPopulation, &res.KeptPopulationLimit); err != nil {
		return fmt.Errorf("recontar la ciudad %d: %w", kept, normalize(err))
	}
	res.KeptCityID = kept
	return nil
}

// takeHole es el hueco libre del vencedor y su presencia, cuando puede tomar.
type takeHole struct {
	slot        int16
	presence    string
	lastOffline *time.Time
}

// takeSlot cuenta, bajo el cerrojo de fundación, las ciudades en pie del
// vencedor (SPEC-CITY-TAKEN RN-CITY-036): con menos de dos y aldeanos para la
// tomada, devuelve su hueco libre; si no, nil y la caída arrasa.
func (r *CityFallRepo) takeSlot(ctx context.Context, tx pgx.Tx, f cityfall.Fall) (*takeHole, error) {
	if len(f.TakeSpawns) == 0 || f.VictorID == uuid.Nil || f.VictorID == f.OwnerID {
		return nil, nil
	}
	rows, err := tx.Query(ctx,
		`SELECT slot, presence_state, last_offline_at FROM cities
		  WHERE owner_player_id = $1 AND fallen_at IS NULL ORDER BY id FOR UPDATE`, f.VictorID)
	if err != nil {
		return nil, fmt.Errorf("leer las ciudades del vencedor: %w", normalize(err))
	}
	defer rows.Close()
	hole := &takeHole{slot: 1}
	enPie := 0
	for rows.Next() {
		var (
			slot int16
			ps   string
			lo   *time.Time
		)
		if err := rows.Scan(&slot, &ps, &lo); err != nil {
			return nil, fmt.Errorf("leer una ciudad del vencedor: %w", normalize(err))
		}
		if enPie == 0 {
			hole.presence, hole.lastOffline = ps, lo
		}
		enPie++
		if slot == 1 {
			hole.slot = 2
		}
	}
	if err := rows.Err(); err != nil {
		return nil, normalize(err)
	}
	if enPie == 0 || enPie >= city.MaxStandingPerOwner {
		return nil, nil
	}
	return hole, nil
}

// takeCity crea la ciudad tomada en el centro de la caída, ya arrasada, y le pasa
// sus edificios completos (ADR-052 §1, §2; INV-CITY-024).
func (r *CityFallRepo) takeCity(ctx context.Context, tx pgx.Tx, f cityfall.Fall, hole *takeHole, res *cityfall.FallResult) error {
	populationCap, err := r.cities.PopulationCapOf(ctx, tx, f.Era)
	if err != nil {
		return err
	}
	tomada := f.Now
	c := &city.City{
		OwnerPlayerID: f.VictorID, Name: f.Name, CenterX: f.Center.X, CenterY: f.Center.Y, Era: f.Era,
		PopulationLimit: populationCap, PresenceState: city.PresenceState(hole.presence), LastOnlineAt: &tomada,
		Slot: hole.slot,
	}
	if _, err := r.cities.Create(ctx, tx, c); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE cities SET taken_from_city_id = $2, last_offline_at = $3 WHERE id = $1`,
		c.ID, f.CityID, hole.lastOffline); err != nil {
		return fmt.Errorf("apuntar de dónde viene la ciudad %d: %w", c.ID, normalize(err))
	}
	c.LastOfflineAt = hole.lastOffline
	if err := CreateCityStocks(ctx, tx, c.ID); err != nil {
		return err
	}
	rows, err := tx.Query(ctx,
		`UPDATE buildings SET city_id = $2 WHERE city_id = $1 AND status = 'COMPLETE' RETURNING id, type`, f.CityID, c.ID)
	if err != nil {
		return fmt.Errorf("pasar los edificios a la ciudad %d: %w", c.ID, normalize(err))
	}
	defer rows.Close()
	var bono int32
	for rows.Next() {
		var (
			id   int64
			tipo string
		)
		if err := rows.Scan(&id, &tipo); err != nil {
			return fmt.Errorf("leer un edificio heredado: %w", normalize(err))
		}
		res.TakenBuildings = append(res.TakenBuildings, id)
		if def, err := building.Lookup(building.Type(tipo)); err == nil {
			bono += def.PopulationBonus
		}
	}
	if err := rows.Err(); err != nil {
		return normalize(err)
	}
	sortInt64(res.TakenBuildings)
	c.PopulationLimit = populationCap + bono
	res.Taken = c
	return nil
}

// settleTaken da a la tomada sus tres aldeanos, el territorio de su centro si
// quedó libre y su población y límite (RN-CITY-037, RN-TERR-007).
func (r *CityFallRepo) settleTaken(ctx context.Context, tx pgx.Tx, f cityfall.Fall, res *cityfall.FallResult) error {
	c := res.Taken
	def, err := unit.Lookup(unit.TypeVillager)
	if err != nil {
		return err
	}
	for _, spawn := range f.TakeSpawns {
		u := &unit.Unit{
			PlayerID: f.VictorID, CityID: &c.ID, Type: unit.TypeVillager, X: spawn.X, Y: spawn.Y,
			HP: def.MaxHP, MaxHP: def.MaxHP, Status: unit.StatusIdle,
		}
		if _, err := r.units.Create(ctx, tx, u); err != nil {
			return err
		}
		res.TakenVillagers = append(res.TakenVillagers, u)
	}
	var territorio int64
	err = tx.QueryRow(ctx,
		`SELECT id FROM territories WHERE $1 BETWEEN min_x AND max_x AND $2 BETWEEN min_y AND max_y ORDER BY id LIMIT 1`,
		c.CenterX, c.CenterY).Scan(&territorio)
	if err != nil && !errors.Is(normalize(err), ErrNotFound) {
		return fmt.Errorf("leer el territorio de la ciudad %d: %w", c.ID, normalize(err))
	}
	if territorio != 0 {
		actual, err := r.territories.GetControl(ctx, tx, territorio)
		if err != nil {
			return fmt.Errorf("leer el control del territorio %d: %w", territorio, err)
		}
		if actual.OwnerType == territory.OwnerNone {
			ctrl, err := r.territories.ClaimForPlayer(ctx, tx, territorio, f.VictorID, f.Now.UTC(), actual.Version)
			if err != nil {
				return err
			}
			res.TakenClaimed = &ctrl
		}
	}
	if err := tx.QueryRow(ctx,
		`UPDATE cities c SET population_limit = $2,
		        population = (SELECT count(*) FROM units u
		                       WHERE u.city_id = c.id AND u.status <> 'DEAD' AND u.unit_type <> 'CARAVAN'),
		        version = version + 1
		  WHERE c.id = $1 RETURNING population, version`, c.ID, c.PopulationLimit).
		Scan(&c.Population, &c.Version); err != nil {
		return fmt.Errorf("recontar la ciudad %d: %w", c.ID, normalize(err))
	}
	return nil
}

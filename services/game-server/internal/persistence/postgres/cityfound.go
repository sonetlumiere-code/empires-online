package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/cityfall"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/economy"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/eventlog"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
)

// FoundCity es la transacción de la fundación de una segunda ciudad
// (SPEC-MULTI-CITY §7, ADR-051 §4): bajo el cerrojo de fundación vuelve a
// comprobar el tope de ciudades en pie (INV-CITY-022), el aldeano y el sitio,
// cobra lo reservado, crea la ciudad en el hueco libre, pasa a ella al fundador
// con dos aldeanos nuevos, gana su territorio si estaba libre y escribe
// CityFounded. Lo que ya no cabe es cityfall.ErrFoundRejected y no escribe nada;
// el almacén que no llega, economy.ErrInsufficientResources.
func (r *CityFallRepo) FoundCity(ctx context.Context, f cityfall.Founding, stock []StockDelta) (*cityfall.FoundingResult, error) {
	if f.Place == nil {
		return nil, fmt.Errorf("la fundación necesita con qué comprobar el sitio")
	}
	if f.Now.IsZero() {
		return nil, fmt.Errorf("la fundación necesita el instante del reloj inyectado")
	}
	def, err := unit.Lookup(unit.TypeVillager)
	if err != nil {
		return nil, err
	}
	for intento := 0; intento < maxPlacementAttempts; intento++ {
		existing, err := r.standingCentersBut(ctx, r.store.pool, 0)
		if err != nil {
			return nil, err
		}
		site, err := f.Place(existing)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", cityfall.ErrFoundRejected, err)
		}
		if len(site.Spawns) != cityfall.FoundVillagers {
			return nil, fmt.Errorf("la fundación necesita %d tiles de nacimiento, llegaron %d",
				cityfall.FoundVillagers, len(site.Spawns))
		}
		result, err := r.found(ctx, f, site, stock, def)
		if !errors.Is(err, errPlacementStale) {
			return result, err
		}
	}
	return nil, ErrPlacementContended
}

func (r *CityFallRepo) found(ctx context.Context, f cityfall.Founding, site cityfall.Site, stock []StockDelta,
	def unit.Definition) (*cityfall.FoundingResult, error) {
	res := &cityfall.FoundingResult{}
	rechazo := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", cityfall.ErrFoundRejected, fmt.Sprintf(format, args...))
	}
	err := r.store.InTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, foundingLockKey); err != nil {
			return fmt.Errorf("serializar la fundación: %w", err)
		}
		if _, err := tx.Exec(ctx, `SET LOCAL eo.population_recount = 'on'`); err != nil {
			return fmt.Errorf("declarar el recuento de población: %w", err)
		}

		// 1. El tope: una sola ciudad en pie, la que paga (RN-CITY-031).
		var (
			enPie       int
			usados      []int16
			eraCapital  city.Era
			presencia   string
			lastOffline *time.Time
		)
		rows, err := tx.Query(ctx,
			`SELECT id, slot, era, presence_state, last_offline_at FROM cities
			  WHERE owner_player_id = $1 AND fallen_at IS NULL ORDER BY id FOR UPDATE`, f.OwnerID)
		if err != nil {
			return fmt.Errorf("leer las ciudades del jugador: %w", normalize(err))
		}
		pagaEnPie := false
		for rows.Next() {
			var (
				id   int64
				slot int16
				era  string
				ps   string
				lo   *time.Time
			)
			if err := rows.Scan(&id, &slot, &era, &ps, &lo); err != nil {
				rows.Close()
				return fmt.Errorf("leer una ciudad del jugador: %w", normalize(err))
			}
			if enPie == 0 {
				eraCapital = city.Era(era)
			}
			enPie++
			usados = append(usados, slot)
			if id == f.FromCityID {
				pagaEnPie, presencia, lastOffline = true, ps, lo
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return normalize(err)
		}
		if !pagaEnPie {
			return rechazo("la ciudad %d ya no está en pie", f.FromCityID)
		}
		// SPEC-MORE-CITIES RN-CITY-040: el tope de la era de la capital.
		if tope := city.MaxStandingFor(eraCapital); enPie >= tope {
			return rechazo("ya tienes %d ciudades: el tope de tu capital es %d", enPie, tope)
		}
		huecoLibre := city.FreeSlot(usados)
		if huecoLibre == 0 {
			return rechazo("no queda hueco")
		}

		// 2. El aldeano sigue vivo, suyo y de la ciudad que paga.
		var (
			tipo, estado string
			ciudadU      *int64
			dueno        uuid.UUID
		)
		if err := tx.QueryRow(ctx,
			`SELECT unit_type, status, city_id, player_id FROM units WHERE id = $1 FOR UPDATE`, f.UnitID).
			Scan(&tipo, &estado, &ciudadU, &dueno); err != nil {
			if errors.Is(normalize(err), ErrNotFound) {
				return rechazo("el aldeano %d no existe", f.UnitID)
			}
			return fmt.Errorf("leer el aldeano %d: %w", f.UnitID, normalize(err))
		}
		if dueno != f.OwnerID || tipo != string(unit.TypeVillager) || estado == string(unit.StatusDead) ||
			ciudadU == nil || *ciudadU != f.FromCityID {
			return rechazo("el aldeano %d ya no puede fundar", f.UnitID)
		}

		// 3. El sitio, otra vez, contra las ciudades confirmadas mientras se buscaba.
		if f.Verify != nil {
			existing, err := r.standingCentersBut(ctx, tx, 0)
			if err != nil {
				return err
			}
			if !f.Verify(site.Center, existing) {
				return errPlacementStale
			}
		}

		// 4. El territorio del sitio, si lo hay, sigue libre (SPEC-MULTI-CITY §4.1).
		if site.TerritoryID != 0 {
			actual, err := r.territories.GetControl(ctx, tx, site.TerritoryID)
			if err != nil {
				return fmt.Errorf("leer el control del territorio %d: %w", site.TerritoryID, err)
			}
			if actual.OwnerType != territory.OwnerNone {
				return rechazo("el territorio %d ya tiene dueño", site.TerritoryID)
			}
			ctrl, err := r.territories.ClaimForPlayer(ctx, tx, site.TerritoryID, f.OwnerID, f.Now.UTC(), actual.Version)
			if err != nil {
				return err
			}
			res.Claimed = &ctrl
		}

		// 5. El cobro de lo reservado (RN-CITY-030).
		for _, d := range stock {
			aplicado, err := applyStock(ctx, tx, d)
			if err != nil {
				return err
			}
			if !aplicado {
				return fmt.Errorf("%w: ciudad %d, %s", ErrStaleStock, d.CityID, d.Resource)
			}
		}

		// 6. La ciudad, en el hueco libre: la era de la que paga, el almacén a
		// cero y la presencia del jugador (RN-CITY-032, RN-CITY-034).
		populationCap, err := r.cities.PopulationCapOf(ctx, tx, f.Era)
		if err != nil {
			return err
		}
		fundada := f.Now
		c := &city.City{
			OwnerPlayerID: f.OwnerID, Name: f.Name, CenterX: site.Center.X, CenterY: site.Center.Y, Era: f.Era,
			PopulationLimit: populationCap, PresenceState: city.PresenceState(presencia), LastOnlineAt: &fundada,
			Slot: huecoLibre,
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

		// 7. El fundador pasa a la ciudad nueva y nacen dos aldeanos más.
		if _, err := tx.Exec(ctx, `UPDATE units SET city_id = $2 WHERE id = $1`, f.UnitID, c.ID); err != nil {
			return fmt.Errorf("pasar el fundador a la ciudad %d: %w", c.ID, normalize(err))
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

		// 8. Población de las dos, como las recuenta la carga (INV-CITY-017).
		for _, id := range []int64{f.FromCityID, c.ID} {
			var pop int32
			if err := tx.QueryRow(ctx,
				`UPDATE cities c SET population = (SELECT count(*) FROM units u
				        WHERE u.city_id = c.id AND u.status <> 'DEAD' AND u.unit_type <> 'CARAVAN'),
				        version = version + 1
				  WHERE c.id = $1 RETURNING population`, id).Scan(&pop); err != nil {
				return fmt.Errorf("recontar la ciudad %d: %w", id, normalize(err))
			}
			if id == c.ID {
				c.Population = pop
			} else {
				res.FromPopulation = pop
			}
		}
		c.Version++

		// 9. El evento, al fundador (SPEC-MULTI-CITY §6).
		fundador := f.OwnerID
		if err := appendEvent(ctx, tx, worldEvent{
			EventType: eventlog.EventCityFounded, Tick: f.Tick, PlayerID: &fundador,
			Payload: map[string]any{
				"cityId": c.ID, "fromCityId": f.FromCityID, "unitId": f.UnitID, "cityName": c.Name,
				"x": c.CenterX, "y": c.CenterY, "atMs": f.AtMs,
			},
		}); err != nil {
			return err
		}
		res.City = c
		return nil
	})
	if err != nil {
		if errors.Is(err, economy.ErrInsufficientResources) || errors.Is(err, cityfall.ErrFoundRejected) ||
			errors.Is(err, errPlacementStale) {
			return nil, err
		}
		if IsUniqueViolation(err) {
			// INV-CITY-022: el índice sobre el hueco, si dos se adelantaran al cerrojo.
			return nil, fmt.Errorf("%w: %v", cityfall.ErrFoundRejected, err)
		}
		return nil, err
	}
	return res, nil
}

package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/advance"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/task"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/loadcheck"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// checkLoadedState valida el estado durable recién leído (EO-119) y devuelve
// las unidades, los controles, las guarniciones y las tareas con los que se
// hidrata la simulación. Las ciudades de in.Cities quedan corregidas en el sitio.
//
// Cada hallazgo emite la señal obligatoria de docs/invariants/README.md §5.1,
// también cuando el arranque aborta: es lo único que dice por qué.
func checkLoadedState(
	ctx context.Context,
	repo *postgres.LoadCheckRepo,
	in loadcheck.Input,
	tick uint64,
	log *slog.Logger,
) ([]*unit.Unit, []territory.Control, map[int64]int64, []task.Task, error) {
	res, err := repo.CheckAndRepair(ctx, in, tick)
	for _, f := range res.Findings {
		log.Error("invariant_violation",
			"inv_id", f.InvID, "severity", f.Severity, "policy", f.Policy,
			"entity", f.Entity, "detail", f.Detail)
	}
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if len(res.Findings) > 0 {
		log.Info("validación de carga",
			"findings", len(res.Findings),
			"cities_repaired", len(res.CityRepairs), "units_repaired", len(res.UnitRepairs),
			"controls_repaired", len(res.ControlRepairs), "garrisons_deleted", len(res.GarrisonDeletes),
			"tasks_deleted", len(res.TaskDeletes),
			"units_excluded", len(in.Units)-len(res.Units))
	}
	return res.Units, res.Controls, res.Garrisons, res.Tasks, nil
}

// loadEconomy lee la economía durable: stocks, tareas, edificios,
// producciones, avances de era y las capas de bosque y colina. Las tareas pasan
// después por la validación de carga.
func loadEconomy(ctx context.Context, repo *postgres.EconomyRepo) (simulation.EconomyLoad, error) {
	var out simulation.EconomyLoad
	stocks, err := repo.ListStocks(ctx)
	if err != nil {
		return out, err
	}
	for _, s := range stocks {
		out.Stocks = append(out.Stocks, simulation.StockRow{CityID: s.CityID, Stock: s.Stock})
	}
	if out.Tasks, err = repo.ListTasks(ctx); err != nil {
		return out, err
	}
	if out.Buildings, err = repo.ListBuildings(ctx); err != nil {
		return out, err
	}
	if out.Productions, err = repo.ListProductions(ctx); err != nil {
		return out, err
	}
	forests, err := repo.ListForests(ctx)
	if err != nil {
		return out, err
	}
	for _, f := range forests {
		out.Forests = append(out.Forests, simulation.DepositRow{Tile: f.Tile, State: f.State})
	}
	hills, err := repo.ListHills(ctx)
	if err != nil {
		return out, err
	}
	for _, h := range hills {
		out.Hills = append(out.Hills, simulation.DepositRow{Tile: h.Tile, State: h.State})
	}
	if out.Advances, err = repo.ListAdvances(ctx); err != nil {
		return out, err
	}
	// Las elaboraciones en curso (ADR-036 §4): una vencida durante la caída
	// se entrega en el primer tick.
	if out.Crafts, err = repo.ListCrafts(ctx); err != nil {
		return out, err
	}
	return out, nil
}

// checkAdvances descarta, con su invariant_violation, los avances que no son
// un paso del catálogo o no salen de la era de su ciudad (INV-CITY-018), y los
// borra de la base: aplicarlos haría retroceder o saltar la era.
func checkAdvances(ctx context.Context, repo *postgres.EconomyRepo, advances []advance.Advance,
	cities []*city.City, log *slog.Logger) ([]advance.Advance, error) {
	eraOf := make(map[int64]city.Era, len(cities))
	for _, c := range cities {
		eraOf[c.ID] = c.Era
	}
	var out []advance.Advance
	for _, a := range advances {
		era, ok := eraOf[a.CityID]
		if err := a.Validate(); err == nil && ok && era == a.From {
			out = append(out, a)
			continue
		}
		log.Error("invariant_violation", "inv_id", "INV-CITY-018", "severity", "ALTO", "policy", "REJECT",
			"entity", fmt.Sprintf("city:%d", a.CityID),
			"detail", fmt.Sprintf("avance de %s a %s con la ciudad en %s: se descarta", a.From, a.To, era))
		if err := repo.DeleteAdvance(ctx, a.CityID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

package main

import (
	"context"
	"log/slog"

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

// loadEconomy lee la economía durable: stocks, tareas, edificios y
// producciones. Las tareas pasan después por la validación de carga.
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
	return out, nil
}

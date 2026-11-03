package simulation_test

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"

	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
)

// Todo trabajo que la simulación encola tiene que estar en el catálogo cerrado
// JobNames (ADR-022): con otro nombre, eo_persistence_jobs_dropped_total lo
// cuenta como `unknown` y su serie no existe desde el arranque. Hasta E6 se
// habían quedado fuera los de combate (unit.death, unit.attacked) y el del
// saqueo (city.pillage). En vez de una lista a mano, cada Submit del arnés
// anota su nombre y, al terminar el paquete, se comparan con el catálogo.

var (
	trabajosMu      sync.Mutex
	trabajosVistos  = map[string]struct{}{}
	catalogoTrabajo = func() map[string]struct{} {
		out := map[string]struct{}{}
		for _, n := range simulation.JobNames() {
			out[n] = struct{}{}
		}
		return out
	}()
)

func anotarTrabajo(name string) {
	trabajosMu.Lock()
	defer trabajosMu.Unlock()
	trabajosVistos[name] = struct{}{}
}

func TestMain(m *testing.M) {
	code := m.Run()
	var fuera []string
	for n := range trabajosVistos {
		if _, ok := catalogoTrabajo[n]; !ok {
			fuera = append(fuera, n)
		}
	}
	sort.Strings(fuera)
	if len(fuera) > 0 {
		fmt.Fprintf(os.Stderr, "trabajos encolados que no están en simulation.JobNames: %v\n", fuera)
		code = 1
	}
	os.Exit(code)
}

// CommandTypes es el catálogo de eo_commands_dropped_total: incluye los
// comandos de combate, saqueo y diplomacia, y sin duplicados.
func TestElCatalogoDeComandosIncluyeCombateSaqueoYDiplomacia(t *testing.T) {
	vistos := map[string]bool{}
	for _, c := range simulation.CommandTypes() {
		if vistos[c] {
			t.Errorf("tipo de comando duplicado en el catálogo: %s", c)
		}
		vistos[c] = true
	}
	for _, cmd := range []simulation.Command{
		simulation.AttackUnit{}, simulation.DeathPersistFailed{},
		simulation.PillageCity{}, simulation.PillageResolved{},
		simulation.ProposeTreaty{}, simulation.RespondTreaty{}, simulation.CancelTreaty{},
		simulation.TreatyResolved{}, simulation.TreatyEndPersistFailed{},
	} {
		if !vistos[simulation.CommandType(cmd)] {
			t.Errorf("%s no está en simulation.CommandTypes", simulation.CommandType(cmd))
		}
	}
}

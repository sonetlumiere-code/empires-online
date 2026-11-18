package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/advance"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/technology"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// loadTechnologies lee y valida el catálogo de tecnologías (INV-TECH-002: uno
// inválido impide arrancar) y lee las tecnologías de cada jugador
// (SPEC-TECHNOLOGIES §7, ADR-046 §1).
func loadTechnologies(ctx context.Context, repo *postgres.EconomyRepo, civs []player.Civilization) (technology.Catalog, map[uuid.UUID][]string, error) {
	filas, err := repo.ListTechnologies(ctx)
	if err != nil {
		return technology.Catalog{}, nil, err
	}
	codigos := make([]string, 0, len(civs))
	for _, c := range civs {
		codigos = append(codigos, c.Code)
	}
	catalogo, err := technology.NewCatalog(filas, codigos)
	if err != nil {
		return technology.Catalog{}, nil, fmt.Errorf("catálogo de tecnologías: %w", err)
	}
	tenidas, err := repo.ListPlayerTechnologies(ctx)
	if err != nil {
		return technology.Catalog{}, nil, err
	}
	return catalogo, tenidas, nil
}

// checkResearch descarta, con su invariant_violation, las investigaciones que
// no cumplen INV-TECH-001 —una ciudad que no existe o que avanza de era, una
// tecnología fuera del catálogo, de otra civilización, de una era posterior,
// ya tenida o sin sus requisitos— y las borra de la base: aplicarlas daría un
// efecto que el jugador no podía tener.
func checkResearch(
	ctx context.Context, repo *postgres.EconomyRepo, research []technology.Research, cities []*city.City,
	advances []advance.Advance, cat technology.Catalog, owned map[uuid.UUID][]string, log *slog.Logger,
) ([]technology.Research, error) {
	porID := make(map[int64]*city.City, len(cities))
	for _, c := range cities {
		porID[c.ID] = c
	}
	avanzando := make(map[int64]bool, len(advances))
	for _, a := range advances {
		avanzando[a.CityID] = true
	}
	var out []technology.Research
	for _, r := range research {
		motivo := researchProblem(r, porID[r.CityID], avanzando[r.CityID], cat, owned)
		if motivo == "" {
			out = append(out, r)
			continue
		}
		log.Error("invariant_violation", "inv_id", "INV-TECH-001", "severity", "ALTO", "policy", "REPAIR",
			"entity", fmt.Sprintf("city:%d", r.CityID),
			"detail", fmt.Sprintf("investigación de %s: %s; se descarta", r.Technology, motivo))
		if err := repo.DeleteResearch(ctx, r.CityID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// researchProblem dice por qué una investigación leída no cumple INV-TECH-001,
// o "" si la cumple.
func researchProblem(r technology.Research, c *city.City, advancing bool, cat technology.Catalog, owned map[uuid.UUID][]string) string {
	if c == nil {
		return "la ciudad no existe"
	}
	if advancing {
		return "la ciudad avanza de era"
	}
	t, ok := cat.Lookup(r.Technology)
	if !ok {
		return "la tecnología no está en el catálogo"
	}
	if !t.AvailableTo(c.Civilization) {
		return "es exclusiva de otra civilización"
	}
	if !c.Era.AtLeast(t.Era) {
		return "pide una era posterior a la de la ciudad"
	}
	tiene := make(map[string]bool, len(owned[c.OwnerPlayerID]))
	for _, code := range owned[c.OwnerPlayerID] {
		tiene[code] = true
	}
	if tiene[t.Code] {
		return "el dueño ya la tiene"
	}
	for _, req := range t.Requires {
		if !tiene[req] {
			return "al dueño le falta " + req
		}
	}
	return ""
}

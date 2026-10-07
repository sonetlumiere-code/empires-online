// Command devseed siembra una base de DESARROLLO con jugadores y safe zones
// para probar el juego a mano. Es EO-114 de docs/roadmap/backlog.md.
//
//	pnpm run dev:seed
//
// Crea los jugadores de devseed.Players por el mismo camino que el alta —
// founding.FindSite y la transacción atómica del Bootstrapper— y coloca un
// DENSE_FOREST junto a cada ciudad. Es idempotente: lo que ya existe se deja.
//
// Condiciones, todas comprobadas antes de escribir nada:
//
//   - EO_ENV=development. En cualquier otro entorno se niega.
//   - El servidor de juego debe estar PARADO. El mundo vive en su RAM: si se
//     sembrara con él en marcha, no conocería a los jugadores ni a las zonas
//     hasta reiniciar, y entretanto podría fundar una ciudad encima.
//   - El servidor tiene que haber arrancado al menos una vez contra esta base:
//     es él quien crea world_state, los chunks y los territorios.
//
// Nunca toca el mundo canónico: no hay migración, y las zonas sólo existen en la
// base a la que apunta EO_POSTGRES_URL. La siembra de zonas del mundo canónico
// es una decisión de diseño pendiente (docs/specs/safe-zones.md §2).
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/config"
	"github.com/empires-online/empires-online/services/game-server/internal/devseed"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/safezone"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/game/founding"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "\n  ✗ %v\n\n", err)
		os.Exit(1)
	}
}

func run() error {
	_, _ = config.LoadDotEnv()
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Env != "development" {
		return fmt.Errorf("EO_ENV=%q: la siembra de desarrollo sólo se ejecuta con EO_ENV=development", cfg.Env)
	}
	if addr := localAddr(cfg.HTTPAddr); serverListening(addr) {
		return fmt.Errorf("hay algo escuchando en %s: para el servidor de juego antes de sembrar "+
			"(el mundo vive en su RAM y no vería lo sembrado hasta reiniciar)", addr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	store, err := postgres.New(ctx, cfg.PostgresURL)
	if err != nil {
		return err
	}
	defer store.Close()

	state, err := postgres.NewWorldRepo(store).Load(ctx)
	if errors.Is(err, postgres.ErrNotFound) {
		return errors.New("esta base no tiene mundo todavía: arranca el servidor una vez (pnpm run server:run), páralo y vuelve a sembrar")
	}
	if err != nil {
		return err
	}
	w, err := world.New(state.Width, state.Height, state.ChunkSize, state.Seed,
		world.Generate(state.Width, state.Height, state.Seed))
	if err != nil {
		return err
	}

	players := postgres.NewPlayerRepo(store)
	cities := postgres.NewCityRepo(store)
	territories := postgres.NewTerritoryRepo(store)
	zones := postgres.NewSafeZoneRepo(store)
	bootstrapper := postgres.NewBootstrapper(store, players, cities,
		postgres.NewUnitRepo(store, cfg.ChunkSize), territories)

	// El mismo estado de ocupación que verá el servidor al arrancar.
	existentes, err := cities.ListAll(ctx)
	if err != nil {
		return err
	}
	centros := make([]world.Tile, 0, len(existentes))
	for _, c := range existentes {
		w.SetBlocked(c.CenterX-1, c.CenterY-1, c.CenterX+1, c.CenterY+1, true)
		centros = append(centros, world.Tile{X: c.CenterX, Y: c.CenterY})
	}

	geometria, err := territories.LoadAll(ctx)
	if err != nil {
		return err
	}
	indiceTerritorios, _, err := territory.BuildSet(geometria, w.Width(), w.Height(), w.ChunkSize())
	if err != nil {
		return err
	}
	eras, err := cities.ListEras(ctx)
	if err != nil || len(eras) == 0 {
		return fmt.Errorf("catálogo de eras vacío o ilegible: %w", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(devseed.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	fmt.Printf("\nSiembra de desarrollo sobre el mundo seed=%d %d×%d\n\n", state.Seed, state.Width, state.Height)

	// ── Jugadores ───────────────────────────────────────────────
	type sembrado struct {
		nombre string
		centro world.Tile
	}
	var jugadores []sembrado
	for _, nombre := range devseed.Players {
		if p, _, err := players.GetByUsername(ctx, nombre); err == nil {
			c, err := cities.GetByOwner(ctx, p.ID)
			if err != nil {
				return fmt.Errorf("%s existe pero no se pudo leer su ciudad: %w", nombre, err)
			}
			jugadores = append(jugadores, sembrado{nombre, world.Tile{X: c.CenterX, Y: c.CenterY}})
			fmt.Printf("  = %-10s ya existía, ciudad en (%d,%d)\n", nombre, c.CenterX, c.CenterY)
			continue
		} else if !errors.Is(err, postgres.ErrNotFound) && !errors.Is(err, player.ErrNotFound) {
			return fmt.Errorf("buscar %s: %w", nombre, err)
		}

		site, err := founding.FindSite(w, centros, devseed.AnchorSeed, 3)
		if err != nil {
			return fmt.Errorf("no hay sitio para %s: %w", nombre, err)
		}
		var territorioID int64
		if t, ok := indiceTerritorios.TerritoryAt(site.Center.X, site.Center.Y); ok {
			territorioID = t.ID
		}
		if _, err := bootstrapper.Create(ctx, postgres.BootstrapRequest{
			Username:       nombre,
			PasswordHash:   string(hash),
			CivilizationID: 1,
			FactionID:      3, // NEUTRAL, como el alta por la API
			CityName:       nombre + "polis",
			CityCenter:     site.Center,
			Era:            eras[0].Code,
			PopulationCap:  eras[0].PopulationCap,
			TerritoryID:    territorioID,
			Tick:           state.CurrentTick,
			Now:            clock.NewSystemClock().Now(),
			VillagerSpawns: site.Spawns,
		}); err != nil {
			return fmt.Errorf("crear %s: %w", nombre, err)
		}
		w.SetBlocked(site.MinX, site.MinY, site.MaxX, site.MaxY, true)
		centros = append(centros, site.Center)
		jugadores = append(jugadores, sembrado{nombre, site.Center})
		fmt.Printf("  + %-10s ciudad en (%d,%d)\n", nombre, site.Center.X, site.Center.Y)
	}

	// ── Zonas ───────────────────────────────────────────────────
	actuales, err := zones.LoadAll(ctx)
	if err != nil {
		return err
	}
	nombres := make(map[string]bool, len(actuales))
	ocupado := make([]devseed.Rect, 0, len(actuales)+len(centros))
	for _, z := range actuales {
		nombres[z.Name] = true
		ocupado = append(ocupado, devseed.ZoneRect(z))
	}
	for _, c := range centros {
		ocupado = append(ocupado, devseed.CityRect(c))
	}

	fmt.Println()
	var sinCaverna int
	for _, j := range jugadores {
		for _, tipo := range []safezone.Type{safezone.DenseForest, safezone.Cavern} {
			nombre := devseed.ZoneName(tipo, j.nombre)
			if nombres[nombre] {
				fmt.Printf("  = %s ya existía\n", nombre)
				continue
			}
			z, n, ok := devseed.PlaceZone(w, j.centro, tipo, ocupado)
			if !ok {
				if tipo == safezone.Cavern {
					sinCaverna++
					continue
				}
				fmt.Printf("  · sin sitio para %s\n", nombre)
				continue
			}
			z.Name = nombre
			if _, err := zones.Insert(ctx, store.Pool(), z); err != nil {
				return err
			}
			ocupado = append(ocupado, devseed.ZoneRect(z))
			fmt.Printf("  + %s: (%d,%d)-(%d,%d), %d tiles\n", nombre, z.MinX, z.MinY, z.MaxX, z.MaxY, n)
		}
	}
	if sinCaverna > 0 {
		fmt.Printf("  · ninguna CAVERN: no hay montañas a menos de %d tiles de estas ciudades\n", devseed.MaxRing)
	}

	// ── Comprobación final: lo que verá el servidor al arrancar ──
	todas, err := zones.LoadAll(ctx)
	if err != nil {
		return err
	}
	idx, rep, err := safezone.BuildIndex(todas, w)
	if err != nil {
		return err
	}
	if len(rep.Overlaps)+len(rep.Rejected)+len(rep.Urban) > 0 {
		return fmt.Errorf("el índice resultante tiene anomalías: %+v", rep)
	}

	fmt.Printf("\n  %d zonas en el índice, sin solapes ni tiles urbanos.\n", idx.Len())
	fmt.Printf("  Usuarios: %s · contraseña: %s\n", strings.Join(devseed.Players, ", "), devseed.Password)
	fmt.Println("  Arranca el servidor (pnpm run server:run) para que cargue lo sembrado.")
	fmt.Println()
	return nil
}

// localAddr convierte ":8080" en "127.0.0.1:8080" para poder sondearlo.
func localAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "127.0.0.1" + addr
	}
	return addr
}

func serverListening(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

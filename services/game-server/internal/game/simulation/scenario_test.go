package simulation_test

// Escenarios como datos y su golden file (EO-115). El formato y las reglas están
// en docs/testing/simulation-tests.md §6.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/movement"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/safezone"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/loop"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
)

var actualizarGolden = flag.Bool("update-golden", false, "regenera los golden files de testdata/scenarios")

const directorioEscenarios = "testdata/scenarios"

// espacioEscenarios es el espacio de nombres de los UUID v5 de los escenarios.
var espacioEscenarios = uuid.MustParse("5ce0a210-0000-4000-8000-000000000115")

type escenario struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	EpochMs     int64  `json:"epochMs"`
	RunTicks    int    `json:"runTicks"`
	Config      struct {
		ProtectionCooldownSec *int `json:"protectionCooldownSec"`
		DisconnectGraceSec    *int `json:"disconnectGraceSec"`
	} `json:"config"`
	World struct {
		Width   int32  `json:"width"`
		Height  int32  `json:"height"`
		Terrain string `json:"terrain"`
		Seed    uint64 `json:"seed"`
		Paint   []struct {
			Terrain string `json:"terrain"`
			rect
		} `json:"paint"`
	} `json:"world"`
	SafeZones []struct {
		Type safezone.Type `json:"type"`
		rect
	} `json:"safeZones"`
	Players []struct {
		Ref  string `json:"ref"`
		City struct {
			X int32 `json:"x"`
			Y int32 `json:"y"`
		} `json:"city"`
	} `json:"players"`
	Units []struct {
		Ref    string `json:"ref"`
		Player string `json:"player"`
		X      int32  `json:"x"`
		Y      int32  `json:"y"`
	} `json:"units"`
	Commands []struct {
		Tick   int         `json:"tick"`
		Type   string      `json:"type"`
		Player string      `json:"player"`
		Unit   string      `json:"unit"`
		Target *world.Tile `json:"target"`
	} `json:"commands"`
}

type rect struct {
	MinX int32 `json:"minX"`
	MinY int32 `json:"minY"`
	MaxX int32 `json:"maxX"`
	MaxY int32 `json:"maxY"`
}

// Lo que fija el golden (§6.3).
type resultadoEscenario struct {
	Emitted   []emitido `json:"emitted"`
	Persisted []string  `json:"persisted"`
	Final     struct {
		Units     []unidadFinal     `json:"units"`
		Movements []movimientoFinal `json:"movements"`
		Cities    []ciudadFinal     `json:"cities"`
	} `json:"final"`
}

type emitido struct {
	Tick      int             `json:"tick"`
	To        string          `json:"to"`
	Type      string          `json:"type"`
	RequestID string          `json:"requestId,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

type unidadFinal struct {
	Ref    string      `json:"ref"`
	ID     int64       `json:"id"`
	X      int32       `json:"x"`
	Y      int32       `json:"y"`
	Status unit.Status `json:"status"`
}

type movimientoFinal struct {
	Unit          string          `json:"unit"`
	ID            int64           `json:"id"`
	Target        world.Tile      `json:"target"`
	StartTimeMs   int64           `json:"startTimeMs"`
	ArrivalTimeMs int64           `json:"arrivalTimeMs"`
	Status        movement.Status `json:"status"`
}

type ciudadFinal struct {
	Owner           string             `json:"owner"`
	ID              int64              `json:"id"`
	PresenceState   city.PresenceState `json:"presenceState"`
	ProtectionUntil *time.Time         `json:"protectionUntil"`
}

func TestLosEscenariosReproducenSuGolden(t *testing.T) {
	ficheros, err := filepath.Glob(filepath.Join(directorioEscenarios, "*.json"))
	require.NoError(t, err)
	var escenarios []string
	for _, f := range ficheros {
		if !strings.HasSuffix(f, ".golden.json") {
			escenarios = append(escenarios, f)
		}
	}
	require.NotEmpty(t, escenarios, "no hay escenarios en %s", directorioEscenarios)

	for _, fichero := range escenarios {
		nombre := strings.TrimSuffix(filepath.Base(fichero), ".json")
		t.Run(nombre, func(t *testing.T) {
			esc := cargarEscenario(t, fichero)
			primera := ejecutarEscenario(t, esc)
			segunda := ejecutarEscenario(t, esc)
			require.Equal(t, string(primera), string(segunda),
				"el mismo escenario dio dos resultados distintos: la simulación no es determinista")

			golden := filepath.Join(directorioEscenarios, nombre+".golden.json")
			if *actualizarGolden {
				require.NoError(t, os.WriteFile(golden, primera, 0o644))
				return
			}
			esperado, err := os.ReadFile(golden)
			require.NoError(t, err, "falta el golden: genéralo con -update-golden y revisa su contenido")
			require.Equal(t, normalizarFinDeLinea(esperado), string(primera),
				"%s ya no coincide. Si el cambio es buscado, regenera con -update-golden y revisa el diff (§6.4)", golden)
		})
	}
}

// normalizarFinDeLinea tolera un checkout con CRLF en Windows.
func normalizarFinDeLinea(b []byte) string {
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

func cargarEscenario(t *testing.T, fichero string) *escenario {
	t.Helper()
	data, err := os.ReadFile(fichero)
	require.NoError(t, err)
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var esc escenario
	require.NoError(t, dec.Decode(&esc), "escenario ilegible: %s", fichero)
	require.NoError(t, validarEscenario(&esc), "escenario inválido: %s", fichero)
	return &esc
}

// validarEscenario rechaza lo que haría el escenario ambiguo antes de ejecutar
// nada (§6.2).
func validarEscenario(esc *escenario) error {
	if esc.RunTicks <= 0 {
		return fmt.Errorf("runTicks debe ser positivo")
	}
	if esc.World.Terrain != "grassland" && esc.World.Terrain != "seeded" {
		return fmt.Errorf("world.terrain %q: se admite grassland o seeded", esc.World.Terrain)
	}
	refs := map[string]string{}
	for _, p := range esc.Players {
		if _, dup := refs[p.Ref]; dup || p.Ref == "" {
			return fmt.Errorf("ref de jugador vacío o repetido: %q", p.Ref)
		}
		refs[p.Ref] = "player"
	}
	for _, u := range esc.Units {
		if _, dup := refs[u.Ref]; dup || u.Ref == "" {
			return fmt.Errorf("ref de unidad vacío o repetido: %q", u.Ref)
		}
		if refs[u.Player] != "player" {
			return fmt.Errorf("la unidad %s nombra un jugador desconocido: %q", u.Ref, u.Player)
		}
		refs[u.Ref] = "unit"
	}
	for i, c := range esc.Commands {
		if c.Tick < 0 || c.Tick >= esc.RunTicks {
			return fmt.Errorf("comando %d: tick %d fuera de [0, %d)", i, c.Tick, esc.RunTicks)
		}
		if refs[c.Player] != "player" {
			return fmt.Errorf("comando %d: jugador desconocido %q", i, c.Player)
		}
		switch c.Type {
		case "unit.move":
			if refs[c.Unit] != "unit" || c.Target == nil {
				return fmt.Errorf("comando %d: unit.move necesita una unidad conocida y un target", i)
			}
		case "unit.cancel_move":
			if refs[c.Unit] != "unit" {
				return fmt.Errorf("comando %d: unit.cancel_move necesita una unidad conocida", i)
			}
		case "connect", "disconnect":
		default:
			return fmt.Errorf("comando %d: tipo %q no admitido", i, c.Type)
		}
	}
	return nil
}

func terrenoPorNombre(nombre string) (world.TerrainType, error) {
	for t := world.TerrainType(0); t.Valid(); t++ {
		if t.String() == nombre {
			return t, nil
		}
	}
	return 0, fmt.Errorf("terreno desconocido %q", nombre)
}

func idDe(clase, ref string) uuid.UUID { return uuid.NewSHA1(espacioEscenarios, []byte(clase+":"+ref)) }

// ejecutarEscenario monta el mundo, aplica los comandos tick a tick y devuelve el
// resultado ya serializado, con los UUID sustituidos por sus ref.
func ejecutarEscenario(t *testing.T, esc *escenario) []byte {
	t.Helper()

	var terrain []byte
	if esc.World.Terrain == "seeded" {
		terrain = world.Generate(esc.World.Width, esc.World.Height, esc.World.Seed)
	} else {
		terrain = make([]byte, int(esc.World.Width)*int(esc.World.Height))
		for i := range terrain {
			terrain[i] = byte(world.Grassland)
		}
	}
	for _, p := range esc.World.Paint {
		tt, err := terrenoPorNombre(p.Terrain)
		require.NoError(t, err)
		for y := p.MinY; y <= p.MaxY; y++ {
			for x := p.MinX; x <= p.MaxX; x++ {
				terrain[int(y)*int(esc.World.Width)+int(x)] = byte(tt)
			}
		}
	}
	w, err := world.New(esc.World.Width, esc.World.Height, 32, esc.World.Seed, terrain)
	require.NoError(t, err)

	state := simulation.NewState(w)
	if len(esc.SafeZones) > 0 {
		zonas := make([]safezone.Zone, 0, len(esc.SafeZones))
		for i, z := range esc.SafeZones {
			zonas = append(zonas, safezone.Zone{
				ID: int64(i + 1), Name: fmt.Sprintf("zona %d", i+1), Type: z.Type,
				MinX: z.MinX, MinY: z.MinY, MaxX: z.MaxX, MaxY: z.MaxY,
			})
		}
		idx, rep, err := safezone.BuildIndex(zonas, w)
		require.NoError(t, err)
		require.Empty(t, rep.Overlaps)
		state.SetSafeZones(idx)
	}

	sustituir := map[string]string{}
	jugadores := map[string]uuid.UUID{}
	sesiones := map[string]uuid.UUID{}
	ciudades := map[string]int64{}
	for i, p := range esc.Players {
		id, sesion := idDe("player", p.Ref), idDe("session", p.Ref)
		jugadores[p.Ref], sesiones[p.Ref] = id, sesion
		sustituir[id.String()] = "player:" + p.Ref
		sustituir[sesion.String()] = "session:" + p.Ref
		c := &city.City{
			ID: int64(i + 1), OwnerPlayerID: id, Name: "Ciudad de " + p.Ref,
			CenterX: p.City.X, CenterY: p.City.Y, Era: city.EraStone,
			PopulationLimit: 20, PresenceState: city.PresenceOnline,
		}
		ciudades[p.Ref] = c.ID
		state.AddCity(c)
	}
	unidades := map[string]int64{}
	refDeUnidad := map[int64]string{}
	for i, u := range esc.Units {
		tile := world.Tile{X: u.X, Y: u.Y}
		require.True(t, w.IsWalkable(tile.X, tile.Y), "la unidad %s parte de un tile intransitable", u.Ref)
		cityID := ciudades[u.Player]
		state.AddUnit(&unit.Unit{
			ID: int64(i + 1), PlayerID: jugadores[u.Player], CityID: &cityID,
			Type: unit.TypeVillager, X: u.X, Y: u.Y, HP: 40, MaxHP: 40, Status: unit.StatusIdle,
		})
		unidades[u.Ref], refDeUnidad[int64(i+1)] = int64(i+1), u.Ref
	}

	segundos := func(v *int, def int) time.Duration {
		if v == nil {
			return time.Duration(def) * time.Second
		}
		return time.Duration(*v) * time.Second
	}
	clk := clock.NewFakeClock(esc.EpochMs)
	rec := &recorder{}
	persister := &syncPersister{}
	commands := make(chan simulation.Command, 256)
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	sim := simulation.New(state, simulation.Deps{
		Clock:       clk,
		Pathfinder:  pathfinding.NewAStar(20000, 256),
		Broadcaster: rec,
		Persister:   persister,
		Repos:       newFakeRepos(),
		Garrisons:   &fakeGarrisons{},
		Inbox: func(c simulation.Command) bool {
			select {
			case commands <- c:
				return true
			default:
				return false
			}
		},
		Log:                discard,
		ProtectionCooldown: segundos(esc.Config.ProtectionCooldownSec, 300),
		DisconnectGrace:    segundos(esc.Config.DisconnectGraceSec, 30),
		PathMaxNodes:       20000,
		PathMaxDistance:    256,
	})
	gameLoop := loop.New(sim, clk, commands, loop.Config{
		TickDuration:       100 * time.Millisecond,
		FlushIntervalTicks: 50,
		MaxCommandsPerTick: 256,
		EpochMs:            esc.EpochMs,
	}, nil, nil, discard)

	var res resultadoEscenario
	// Listas vacías como [], no null: el golden se lee mejor y no cambia de forma
	// cuando un escenario deja de emitir algo.
	res.Emitted, res.Persisted = []emitido{}, []string{}
	res.Final.Units, res.Final.Movements, res.Final.Cities = []unidadFinal{}, []movimientoFinal{}, []ciudadFinal{}
	siguiente := 0
	for tick := 0; tick < esc.RunTicks; tick++ {
		for i, c := range esc.Commands {
			if c.Tick != tick {
				continue
			}
			requestID := fmt.Sprintf("00000000-0000-4000-8000-%012d", i+1)
			player, sesion := jugadores[c.Player], sesiones[c.Player]
			switch c.Type {
			case "unit.move":
				commands <- simulation.MoveUnit{PlayerID: player, SessionID: sesion, RequestID: requestID,
					UnitID: unidades[c.Unit], Target: *c.Target}
			case "unit.cancel_move":
				commands <- simulation.CancelMovement{PlayerID: player, SessionID: sesion, RequestID: requestID,
					UnitID: unidades[c.Unit]}
			case "connect":
				commands <- simulation.PlayerConnected{PlayerID: player, SessionID: sesion}
			case "disconnect":
				commands <- simulation.PlayerDisconnected{PlayerID: player, SessionID: sesion}
			}
		}
		clk.SetMs(esc.EpochMs + int64(tick)*100)
		gameLoop.Step(clk.NowMs())

		rec.mu.Lock()
		for _, m := range rec.messages[siguiente:] {
			payload, err := json.Marshal(m.Payload)
			require.NoError(t, err)
			res.Emitted = append(res.Emitted, emitido{
				Tick: tick, To: destino(m), Type: m.Type, RequestID: m.RequestID, Payload: payload,
			})
		}
		siguiente = len(rec.messages)
		rec.mu.Unlock()
	}

	res.Persisted = append(res.Persisted, persister.names()...)
	state.EachUnit(func(u *unit.Unit) bool {
		res.Final.Units = append(res.Final.Units, unidadFinal{
			Ref: refDeUnidad[u.ID], ID: u.ID, X: u.X, Y: u.Y, Status: u.Status,
		})
		return true
	})
	state.EachMovement(func(m *movement.Movement) bool {
		res.Final.Movements = append(res.Final.Movements, movimientoFinal{
			Unit: refDeUnidad[m.UnitID], ID: m.ID, Target: m.Target,
			StartTimeMs: m.StartTimeMs, ArrivalTimeMs: m.ArrivalTimeMs, Status: m.Status,
		})
		return true
	})
	state.EachCity(func(c *city.City) bool {
		res.Final.Cities = append(res.Final.Cities, ciudadFinal{
			Owner: c.OwnerPlayerID.String(), ID: c.ID, PresenceState: c.PresenceState,
			ProtectionUntil: c.ProtectionUntil,
		})
		return true
	})
	// Los recorridos del estado ya son estables; ordenar aquí hace que el golden
	// no dependa de esa propiedad, que ya prueba TestSimulacionEsReproducible.
	sort.Slice(res.Final.Units, func(i, j int) bool { return res.Final.Units[i].ID < res.Final.Units[j].ID })
	sort.Slice(res.Final.Movements, func(i, j int) bool { return res.Final.Movements[i].ID < res.Final.Movements[j].ID })
	sort.Slice(res.Final.Cities, func(i, j int) bool { return res.Final.Cities[i].ID < res.Final.Cities[j].ID })

	out, err := json.MarshalIndent(res, "", "  ")
	require.NoError(t, err)
	texto := string(out)
	for id, ref := range sustituir {
		texto = strings.ReplaceAll(texto, id, ref)
	}
	return []byte(texto + "\n")
}

func destino(m capturedMessage) string {
	switch m.Kind {
	case "chunk":
		return fmt.Sprintf("chunk %d,%d", m.CX, m.CY)
	case "chunk-except":
		return fmt.Sprintf("chunk %d,%d except %s", m.CX, m.CY, m.PlayerID)
	case "chunks":
		partes := make([]string, 0, len(m.Chunks))
		for _, c := range m.Chunks {
			partes = append(partes, fmt.Sprintf("%d,%d", c.CX, c.CY))
		}
		return "chunks " + strings.Join(partes, " ")
	default:
		return m.PlayerID.String()
	}
}

// Un escenario ambiguo se rechaza antes de ejecutar nada (§6.2).
func TestUnEscenarioInvalidoSeRechazaAntesDeEjecutar(t *testing.T) {
	base := func() *escenario {
		var esc escenario
		require.NoError(t, json.Unmarshal([]byte(`{
			"name": "x", "epochMs": 1, "runTicks": 10,
			"world": { "width": 64, "height": 64, "terrain": "grassland" },
			"players": [ { "ref": "p1", "city": { "x": 1, "y": 1 } } ],
			"units": [ { "ref": "u1", "player": "p1", "x": 2, "y": 2 } ],
			"commands": [ { "tick": 0, "type": "unit.move", "player": "p1", "unit": "u1", "target": { "x": 3, "y": 3 } } ]
		}`), &esc))
		return &esc
	}
	require.NoError(t, validarEscenario(base()))

	for nombre, estropear := range map[string]func(*escenario){
		"sin duración":          func(e *escenario) { e.RunTicks = 0 },
		"terreno desconocido":   func(e *escenario) { e.World.Terrain = "lava" },
		"jugador repetido":      func(e *escenario) { e.Players = append(e.Players, e.Players[0]) },
		"unidad de nadie":       func(e *escenario) { e.Units[0].Player = "p9" },
		"comando tras el final": func(e *escenario) { e.Commands[0].Tick = 10 },
		"comando de otro":       func(e *escenario) { e.Commands[0].Player = "p9" },
		"move sin destino":      func(e *escenario) { e.Commands[0].Target = nil },
		"tipo de comando ajeno": func(e *escenario) { e.Commands[0].Type = "unit.garrison" },
		"unidad desconocida":    func(e *escenario) { e.Commands[0].Unit = "u9" },
	} {
		esc := base()
		estropear(esc)
		require.Error(t, validarEscenario(esc), nombre)
	}
}

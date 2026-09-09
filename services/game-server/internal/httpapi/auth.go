// Package httpapi expone los endpoints HTTP que no forman parte de la simulación
// en tiempo real: alta de jugador, inicio de sesión y emisión del game ticket.
//
// En la arquitectura objetivo estos endpoints viven en Next.js (ADR-010). Existen
// aquí para que el vertical slice sea ejecutable de extremo a extremo sin depender
// del frontend, y para documentar con código exactamente qué contrato debe cumplir
// el emisor de tickets. Están claramente delimitados y son sustituibles.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/empires-online/empires-online/services/game-server/internal/auth"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/game/founding"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// AuthAPI implementa alta, inicio de sesión y emisión de tickets.
type AuthAPI struct {
	players      *postgres.PlayerRepo
	cities       *postgres.CityRepo
	bootstrapper *postgres.Bootstrapper
	issuer       *auth.Issuer
	world        *world.World
	commands     chan<- simulation.Command
	log          *slog.Logger

	defaultEra   city.Era
	defaultCap   int32
	villagers    int
	territories  *territory.Set
	tick         func() uint64
	civilization int32
	faction      int32
}

// Options configura la API.
type Options struct {
	DefaultEra    city.Era
	PopulationCap int32
	// InitialVillagers es cuántos aldeanos recibe un jugador nuevo (3 en el MVP).
	InitialVillagers int
	CivilizationID   int32
	// Territories es la geometría de los territorios. Es INMUTABLE tras la
	// hidratación, así que leerla desde una goroutine de HTTP no compite con el
	// game loop; el control, que sí muta, no se lee aquí.
	Territories *territory.Set
	// Tick devuelve el tick actual del game loop. Es seguro llamarlo desde
	// cualquier goroutine: el contador del loop es atómico.
	Tick      func() uint64
	FactionID int32
}

// NewAuthAPI construye la API.
func NewAuthAPI(
	players *postgres.PlayerRepo,
	cities *postgres.CityRepo,
	bootstrapper *postgres.Bootstrapper,
	issuer *auth.Issuer,
	w *world.World,
	commands chan<- simulation.Command,
	opts Options,
	log *slog.Logger,
) *AuthAPI {
	if opts.InitialVillagers <= 0 {
		opts.InitialVillagers = 3
	}
	return &AuthAPI{
		players: players, cities: cities, bootstrapper: bootstrapper,
		issuer: issuer, world: w, commands: commands, log: log,
		defaultEra: opts.DefaultEra, defaultCap: opts.PopulationCap,
		villagers:    opts.InitialVillagers,
		civilization: opts.CivilizationID, faction: opts.FactionID,
		territories: opts.Territories,
		tick:        opts.Tick,
	}
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type ticketResponse struct {
	PlayerID string `json:"playerId"`
	Ticket   string `json:"ticket"`
	// ExpiresInSeconds recuerda al cliente que el ticket es efímero y de un solo uso.
	ExpiresInSeconds int           `json:"expiresInSeconds"`
	City             *cityResponse `json:"city,omitempty"`
}

type cityResponse struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	CenterX int32  `json:"centerX"`
	CenterY int32  `json:"centerY"`
}

// Register da de alta un jugador con su ciudad y sus aldeanos iniciales.
func (a *AuthAPI) Register() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "usa POST")
			return
		}
		var creds credentials
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&creds); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_MESSAGE", "cuerpo inválido")
			return
		}
		if err := player.ValidateUsername(creds.Username); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_USERNAME",
				"el nombre debe tener entre 3 y 24 caracteres alfanuméricos, guion o guion bajo")
			return
		}
		if len(creds.Password) < 8 {
			writeError(w, http.StatusBadRequest, "WEAK_PASSWORD", "la contraseña debe tener al menos 8 caracteres")
			return
		}

		ctx := r.Context()
		hash, err := bcrypt.GenerateFromPassword([]byte(creds.Password), bcrypt.DefaultCost)
		if err != nil {
			a.log.Error("no se pudo derivar el hash de la contraseña", "err", err)
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "error interno")
			return
		}

		// Emplazamiento determinista: derivado del nombre de usuario, para que dos
		// altas simultáneas no compitan por el mismo tile.
		existing, err := a.existingCityCenters(ctx)
		if err != nil {
			a.log.Error("no se pudieron leer las ciudades existentes", "err", err)
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "error interno")
			return
		}
		site, err := founding.FindSite(a.world, existing, hashSeed(creds.Username), a.villagers)
		if err != nil {
			writeError(w, http.StatusConflict, "NO_SITE_AVAILABLE", "no queda sitio para una ciudad nueva")
			return
		}

		result, err := a.bootstrapper.Create(ctx, postgres.BootstrapRequest{
			Username:       creds.Username,
			PasswordHash:   string(hash),
			CivilizationID: a.civilization,
			FactionID:      a.faction,
			CityName:       creds.Username + "polis",
			CityCenter:     site.Center,
			Era:            a.defaultEra,
			PopulationCap:  a.defaultCap,
			TerritoryID:    a.territoryAt(site.Center),
			Tick:           a.currentTick(),
			VillagerSpawns: site.Spawns,
		})
		if err != nil {
			if errors.Is(err, player.ErrUsernameTaken) {
				writeError(w, http.StatusConflict, "USERNAME_TAKEN", "ese nombre ya está en uso")
				return
			}
			a.log.Error("alta de jugador fallida", "err", err, "username", creds.Username)
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo crear el jugador")
			return
		}

		// El mundo en RAM sólo conoce al jugador DESPUÉS de que la transacción
		// haya confirmado. Nunca antes.
		a.dispatch(simulation.IntroducePlayer{
			City:        result.City,
			Units:       result.Units,
			BlockedMinX: site.MinX, BlockedMinY: site.MinY,
			BlockedMaxX: site.MaxX, BlockedMaxY: site.MaxY,
			TerritoryControl: result.TerritoryControl,
		})

		ticket, err := a.issuer.Issue(result.Player.ID)
		if err != nil {
			a.log.Error("no se pudo emitir el ticket", "err", err)
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "error interno")
			return
		}

		writeJSON(w, http.StatusCreated, ticketResponse{
			PlayerID:         result.Player.ID.String(),
			Ticket:           ticket,
			ExpiresInSeconds: 60,
			City: &cityResponse{
				ID: result.City.ID, Name: result.City.Name,
				CenterX: result.City.CenterX, CenterY: result.City.CenterY,
			},
		})
	}
}

// Login verifica credenciales y emite un game ticket.
func (a *AuthAPI) Login() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "usa POST")
			return
		}
		var creds credentials
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&creds); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_MESSAGE", "cuerpo inválido")
			return
		}

		ctx := r.Context()
		p, hash, err := a.players.GetByUsername(ctx, creds.Username)
		if err != nil {
			// Mismo error y mismo tiempo aproximado que una contraseña incorrecta:
			// no se revela qué nombres de usuario existen.
			bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinvali"), []byte(creds.Password))
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "credenciales inválidas")
			return
		}
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(creds.Password)); err != nil {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "credenciales inválidas")
			return
		}

		ticket, err := a.issuer.Issue(p.ID)
		if err != nil {
			a.log.Error("no se pudo emitir el ticket", "err", err)
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "error interno")
			return
		}

		resp := ticketResponse{PlayerID: p.ID.String(), Ticket: ticket, ExpiresInSeconds: 60}
		if c, err := a.cities.GetByOwner(ctx, p.ID); err == nil {
			resp.City = &cityResponse{ID: c.ID, Name: c.Name, CenterX: c.CenterX, CenterY: c.CenterY}
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func (a *AuthAPI) existingCityCenters(ctx context.Context) ([]world.Tile, error) {
	cities, err := a.cities.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]world.Tile, 0, len(cities))
	for _, c := range cities {
		out = append(out, world.Tile{X: c.CenterX, Y: c.CenterY})
	}
	return out, nil
}

func (a *AuthAPI) dispatch(cmd simulation.Command) {
	select {
	case a.commands <- cmd:
	case <-time.After(2 * time.Second):
		a.log.Error("no se pudo introducir al jugador en el mundo: cola de comandos saturada")
	}
}

// hashSeed deriva una semilla estable de una cadena (FNV-1a de 64 bits).
func hashSeed(s string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorBody{Code: code, Message: message})
}

// territoryAt resuelve qué territorio contiene el tile, o 0 si ninguno.
//
// Consulta sólo la GEOMETRÍA, que es inmutable desde la hidratación: por eso se
// puede leer desde la goroutine de HTTP sin competir con el game loop. El
// control, que sí muta cada vez que alguien funda, no se lee aquí — lo resuelve
// la propia transacción de alta, que es quien puede hacerlo sin carreras.
func (a *AuthAPI) territoryAt(center world.Tile) int64 {
	if a.territories == nil {
		return 0
	}
	t, ok := a.territories.TerritoryAt(center.X, center.Y)
	if !ok {
		return 0
	}
	return t.ID
}

// currentTick devuelve el tick del loop, o 0 si no se configuró la fuente.
func (a *AuthAPI) currentTick() uint64 {
	if a.tick == nil {
		return 0
	}
	return a.tick()
}

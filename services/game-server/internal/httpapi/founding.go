// Package httpapi expone la única superficie HTTP del Game Server que no es la
// simulación en tiempo real: la fundación de un jugador en el mundo.
//
// Desde ADR-018 el Game Server no conoce contraseñas ni sesiones web. La
// identidad vive en apps/web, que autentica al usuario, elige el id del jugador
// y llama aquí, con un token de fundación, para que el mundo lo cree. Lo que no
// puede salir de este proceso —emplazamiento contra el mapa en RAM, muralla,
// territorio, ciudad y aldeanos en una transacción, y la introducción en el
// mundo tras el COMMIT— sigue aquí.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/auth"
	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/city"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/player"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/game/founding"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
)

// FoundPath es la ruta del endpoint interno de fundación.
const FoundPath = "/internal/players"

// LookupPattern es el patrón de la consulta de un jugador (ADR-020): sólo lee,
// nunca funda.
const LookupPattern = "GET /internal/players/{playerId}"

// FoundingAPI funda jugadores en el mundo a petición de apps/web.
type FoundingAPI struct {
	players      *postgres.PlayerRepo
	cities       *postgres.CityRepo
	bootstrapper *postgres.Bootstrapper
	authn        *auth.Authenticator
	world        *world.World
	commands     chan<- simulation.Command
	log          *slog.Logger

	defaultEra   city.Era
	territories  *territory.Set
	tick         func() uint64
	clock        clock.Clock
	civilization int32
	faction      int32
}

// Options configura la API.
type Options struct {
	DefaultEra     city.Era
	CivilizationID int32
	FactionID      int32
	// Territories es la geometría de los territorios. Es INMUTABLE tras la
	// hidratación, así que leerla desde una goroutine de HTTP no compite con el
	// game loop; el control, que sí muta, no se lee aquí.
	Territories *territory.Set
	// Tick devuelve el tick actual del game loop. Es seguro llamarlo desde
	// cualquier goroutine: el contador del loop es atómico.
	Tick func() uint64
	// Clock fecha el alta (captured_at del territorio reclamado). Si es nil se
	// usa el reloj del sistema, que es lo que hace producción.
	Clock clock.Clock
}

// NewFoundingAuthenticator construye el autenticador de la fundación: sólo
// acepta la audiencia auth.InternalAudience y consume el jti en el mismo almacén
// anti-replay que el ticket de juego. Con la audiencia del ticket, cualquier
// jugador conectado podría fundar jugadores (INV-SEC-008).
func NewFoundingAuthenticator(secret string, clk clock.Clock, consumer auth.TicketConsumer) *auth.Authenticator {
	return auth.NewAuthenticator(auth.NewVerifierFor(secret, clk, auth.InternalAudience), consumer)
}

// NewFoundingAPI construye la API. authn debe salir de NewFoundingAuthenticator.
func NewFoundingAPI(
	players *postgres.PlayerRepo,
	cities *postgres.CityRepo,
	bootstrapper *postgres.Bootstrapper,
	authn *auth.Authenticator,
	w *world.World,
	commands chan<- simulation.Command,
	opts Options,
	log *slog.Logger,
) *FoundingAPI {
	if opts.Clock == nil {
		opts.Clock = clock.NewSystemClock()
	}
	return &FoundingAPI{
		players: players, cities: cities, bootstrapper: bootstrapper,
		authn: authn, world: w, commands: commands, log: log,
		defaultEra:   opts.DefaultEra,
		civilization: opts.CivilizationID, faction: opts.FactionID,
		territories: opts.Territories,
		tick:        opts.Tick,
		clock:       opts.Clock,
	}
}

type foundRequest struct {
	PlayerID string `json:"playerId"`
	Username string `json:"username"`
}

type foundResponse struct {
	PlayerID string       `json:"playerId"`
	City     cityResponse `json:"city"`
}

type cityResponse struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	CenterX int32  `json:"centerX"`
	CenterY int32  `json:"centerY"`
}

// Found funda un jugador: 201 si lo crea, 200 si ya existía con ese id y ese
// nombre. Es idempotente por playerId para que apps/web pueda repetirla tras un
// fallo intermedio sin crear dos mundos para una cuenta (INV-PLAYER-009).
func (a *FoundingAPI) Found() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "usa POST")
			return
		}

		ctx := r.Context()
		claims, ok := a.authenticate(ctx, w, r)
		if !ok {
			return
		}

		var req foundRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_MESSAGE", "cuerpo inválido")
			return
		}
		playerID, err := uuid.Parse(req.PlayerID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_MESSAGE", "playerId no es un uuid")
			return
		}
		// El token autoriza a fundar UN id, el de su sub: no cualquiera.
		if playerID != claims.PlayerID {
			a.log.Warn("fundación rechazada: el cuerpo no nombra el sub del token",
				"jti", claims.JTI, "player_id", playerID)
			writeError(w, http.StatusForbidden, "FORBIDDEN", "el token no autoriza ese jugador")
			return
		}
		if err := player.ValidateUsername(req.Username); err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_USERNAME",
				"el nombre debe tener entre 3 y 24 caracteres alfanuméricos, guion o guion bajo")
			return
		}

		// Idempotencia: una repetición tras un fallo intermedio encuentra el
		// jugador ya confirmado y no lo vuelve a introducir en el mundo.
		if done, ok := a.existing(ctx, w, playerID, req.Username); ok || done {
			return
		}

		var site founding.Site
		result, err := a.bootstrapper.Create(ctx, postgres.BootstrapRequest{
			PlayerID:       playerID,
			Username:       req.Username,
			CivilizationID: a.civilization,
			FactionID:      a.faction,
			CityName:       req.Username + "polis",
			Era:            a.defaultEra,
			Tick:           a.currentTick(),
			Now:            a.clock.Now(),
			// Emplazamiento determinista, derivado del nombre. Se busca fuera de la
			// transacción (RN-PLAYER-014) y se vuelve a comprobar dentro, bajo el
			// cerrojo de fundación: sin esa segunda comprobación, dos altas
			// simultáneas podían fundar a menos de la distancia mínima
			// (INV-CITY-008).
			Place: func(existing []world.Tile) (postgres.Placement, error) {
				s, err := founding.FindSite(a.world, existing, hashSeed(req.Username), player.InitialVillagers)
				if err != nil {
					return postgres.Placement{}, err
				}
				site = s
				return postgres.Placement{
					Center: s.Center, Spawns: s.Spawns, TerritoryID: a.territoryAt(s.Center),
				}, nil
			},
			Verify: founding.FarEnough,
		})
		if err != nil {
			a.foundingFailed(ctx, w, err, playerID, req.Username)
			return
		}

		// El mundo en RAM sólo conoce al jugador DESPUÉS de que la transacción
		// haya confirmado. Nunca antes (INV-PLAYER-008).
		a.dispatch(simulation.IntroducePlayer{
			City:        result.City,
			Units:       result.Units,
			BlockedMinX: site.MinX, BlockedMinY: site.MinY,
			BlockedMaxX: site.MaxX, BlockedMaxY: site.MaxY,
			TerritoryControl: result.TerritoryControl,
		})
		a.log.Info("jugador fundado", "player_id", playerID, "username", req.Username,
			"city_id", result.City.ID)

		writeJSON(w, http.StatusCreated, foundResponse{
			PlayerID: playerID.String(),
			City: cityResponse{
				ID: result.City.ID, Name: result.City.Name,
				CenterX: result.City.CenterX, CenterY: result.City.CenterY,
			},
		})
	}
}

type lookupResponse struct {
	PlayerID string       `json:"playerId"`
	Username string       `json:"username"`
	City     cityResponse `json:"city"`
}

// Lookup responde si un jugador existe en el mundo: 200 con su nombre y su
// ciudad, o 404 PLAYER_NOT_FOUND. Lo usa apps/web para resolver una cuenta
// PENDING abandonada sin fundar nada (ADR-020): si el jugador existe, la cuenta
// se activa; si no, se puede borrar sin dejar un jugador huérfano
// (INV-PLAYER-010). Se autentica con el mismo token de fundación, cuyo sub debe
// ser el jugador consultado.
func (a *FoundingAPI) Lookup() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		claims, ok := a.authenticate(ctx, w, r)
		if !ok {
			return
		}
		playerID, err := uuid.Parse(r.PathValue("playerId"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "INVALID_MESSAGE", "playerId no es un uuid")
			return
		}
		if playerID != claims.PlayerID {
			a.log.Warn("consulta rechazada: la ruta no nombra el sub del token",
				"jti", claims.JTI, "player_id", playerID)
			writeError(w, http.StatusForbidden, "FORBIDDEN", "el token no autoriza ese jugador")
			return
		}

		p, err := a.players.GetByID(ctx, playerID)
		if errors.Is(err, player.ErrNotFound) {
			writeError(w, http.StatusNotFound, "PLAYER_NOT_FOUND", "ese jugador no existe en el mundo")
			return
		}
		if err != nil {
			a.log.Error("no se pudo consultar el jugador", "err", err, "player_id", playerID)
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "error interno")
			return
		}
		c, err := a.cities.GetByOwner(ctx, playerID)
		if err != nil {
			a.log.Error("jugador sin ciudad legible", "err", err, "player_id", playerID)
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "error interno")
			return
		}
		writeJSON(w, http.StatusOK, lookupResponse{
			PlayerID: playerID.String(),
			Username: p.Username,
			City:     cityResponse{ID: c.ID, Name: c.Name, CenterX: c.CenterX, CenterY: c.CenterY},
		})
	}
}

// authenticate verifica el token de fundación y consume su jti. Responde y
// devuelve false si no vale.
func (a *FoundingAPI) authenticate(ctx context.Context, w http.ResponseWriter, r *http.Request) (auth.Claims, bool) {
	header := r.Header.Get("Authorization")
	token, found := strings.CutPrefix(header, "Bearer ")
	if !found || token == "" {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "falta el token de fundación")
		return auth.Claims{}, false
	}
	claims, err := a.authn.Authenticate(ctx, token)
	if err == nil {
		return claims, true
	}
	if errors.Is(err, auth.ErrInvalidTicket) || errors.Is(err, auth.ErrExpiredTicket) ||
		errors.Is(err, auth.ErrReplayedTicket) {
		// El motivo se registra; el token, nunca (INV-PLAYER-007).
		a.log.Warn("fundación rechazada: token no válido", "reason", err.Error())
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", "token de fundación no válido")
		return auth.Claims{}, false
	}
	// No se pudo comprobar si el jti ya se usó: se falla cerrado, como el
	// handshake (INV-SEC-003).
	a.log.Error("fundación rechazada: no se pudo verificar el token", "err", err)
	writeError(w, http.StatusServiceUnavailable, "INTERNAL_ERROR", "no se pudo verificar el token")
	return auth.Claims{}, false
}

// existing responde si el jugador ya existe. Devuelve ok=true si respondió con
// el jugador existente, y done=true si respondió con un error.
func (a *FoundingAPI) existing(ctx context.Context, w http.ResponseWriter, id uuid.UUID, username string) (done, ok bool) {
	p, err := a.players.GetByID(ctx, id)
	if errors.Is(err, player.ErrNotFound) {
		return false, false
	}
	if err != nil {
		a.log.Error("no se pudo consultar el jugador", "err", err, "player_id", id)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "error interno")
		return true, false
	}
	if p.Username != username {
		// El id ya es de otro nombre: nada legítimo lo produce, porque apps/web
		// genera un id nuevo por cuenta. No se reasigna nada.
		a.log.Error("fundación rechazada: el id ya pertenece a otro nombre",
			"player_id", id, "username", username)
		writeError(w, http.StatusConflict, "PLAYER_CONFLICT", "ese jugador ya existe con otro nombre")
		return true, false
	}
	c, err := a.cities.GetByOwner(ctx, id)
	if err != nil {
		a.log.Error("jugador sin ciudad legible", "err", err, "player_id", id)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "error interno")
		return true, false
	}
	writeJSON(w, http.StatusOK, foundResponse{
		PlayerID: id.String(),
		City:     cityResponse{ID: c.ID, Name: c.Name, CenterX: c.CenterX, CenterY: c.CenterY},
	})
	return false, true
}

// foundingFailed traduce el error del bootstrap a la respuesta.
func (a *FoundingAPI) foundingFailed(ctx context.Context, w http.ResponseWriter, err error, id uuid.UUID, username string) {
	switch {
	case errors.Is(err, founding.ErrNoSite):
		writeError(w, http.StatusConflict, "NO_SITE_AVAILABLE", "no queda sitio para una ciudad nueva")
	case errors.Is(err, postgres.ErrPlacementContended):
		// Otras altas simultáneas ocuparon cada sitio elegido. No es un fallo del
		// servidor ni falta de sitio: reintentar en un momento lo resuelve.
		a.log.Warn("alta sin emplazamiento estable tras varios intentos", "err", err, "username", username)
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "SITE_CONTENDED",
			"otras altas simultáneas ocuparon el sitio; vuelve a intentarlo")
	case errors.Is(err, player.ErrUsernameTaken):
		// O el nombre es de otro jugador, o una fundación simultánea del MISMO
		// jugador ganó la carrera: en ese caso la respuesta es la del existente.
		if done, ok := a.existing(ctx, w, id, username); done || ok {
			return
		}
		writeError(w, http.StatusConflict, "USERNAME_TAKEN", "ese nombre ya está en uso")
	default:
		a.log.Error("fundación fallida", "err", err, "username", username)
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "no se pudo crear el jugador")
	}
}

func (a *FoundingAPI) dispatch(cmd simulation.Command) {
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
func (a *FoundingAPI) territoryAt(center world.Tile) int64 {
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
func (a *FoundingAPI) currentTick() uint64 {
	if a.tick == nil {
		return 0
	}
	return a.tick()
}

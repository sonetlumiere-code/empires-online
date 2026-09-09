// Command server es el Game Server autoritativo de Empires Online.
//
// Proceso independiente del frontend. Posee el mundo, el game loop y la verdad.
// Ver ../../../../docs/architecture/game-server.md
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/empires-online/empires-online/services/game-server/internal/auth"
	"github.com/empires-online/empires-online/services/game-server/internal/clock"
	"github.com/empires-online/empires-online/services/game-server/internal/config"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/game/loop"
	"github.com/empires-online/empires-online/services/game-server/internal/game/simulation"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/httpapi"
	"github.com/empires-online/empires-online/services/game-server/internal/observability"
	"github.com/empires-online/empires-online/services/game-server/internal/pathfinding"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence"
	"github.com/empires-online/empires-online/services/game-server/internal/persistence/postgres"
	ws "github.com/empires-online/empires-online/services/game-server/internal/websocket"
)

const (
	commandQueueSize = 8192
	shutdownTimeout  = 25 * time.Second
	ticketTTL        = 60 * time.Second
	ticketReplayTTL  = 120 * time.Second
	idempotencyTTL   = 300 * time.Second
)

func main() {
	if err := run(); err != nil {
		// El logger puede no existir todavía si falló la configuración.
		fmt.Fprintf(os.Stderr, "fallo fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Comodidad de desarrollo: si hay un .env cerca, se carga. Nunca pisa una
	// variable ya definida, y en producción sencillamente no habrá archivo.
	dotenvPath, dotenvErr := config.LoadDotEnv()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := observability.NewLogger(cfg.LogLevel, cfg.Env)
	slog.SetDefault(log)
	metrics := observability.NewMetrics()

	if dotenvErr != nil {
		log.Warn("no se pudo leer el archivo .env", "err", dotenvErr)
	} else if dotenvPath != "" {
		log.Info("configuración cargada desde archivo", "path", dotenvPath)
	}

	log.Info("arrancando Empires Online game server",
		"env", cfg.Env,
		"tick_rate_hz", cfg.TickRateHz,
		"world", fmt.Sprintf("%dx%d", cfg.WorldWidth, cfg.WorldHeight),
		"chunk_size", cfg.ChunkSize)

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ── 1. Migraciones ──────────────────────────────────────────
	//
	// En producción NO se migra al arrancar: varias instancias levantándose a la
	// vez competirían por el mismo esquema. Allí se ejecuta `cmd/migrate` como
	// paso previo del despliegue. Ver docs/operations/deployment.md y ADR-012.
	if cfg.MigrateOnStart {
		if err := postgres.Migrate(cfg.PostgresURL, log); err != nil {
			return fmt.Errorf("migraciones: %w", err)
		}
	} else {
		log.Info("migración automática desactivada; se asume el esquema ya migrado",
			"hint", "ejecuta el binario `migrate` antes de desplegar")
	}

	// ── 2. Dependencias con estado ──────────────────────────────
	store, err := postgres.New(rootCtx, cfg.PostgresURL)
	if err != nil {
		return err
	}
	defer store.Close()

	// Estado caliente: Redis, o en proceso si no hay Redis configurado.
	//
	// La variante en memoria da las mismas garantías que Redis mientras haya UN
	// solo proceso, que es el caso en desarrollo. La configuración ya rechaza
	// esta opción en producción, donde varias instancias romperían el anti-replay.
	hot, err := newHotState(rootCtx, cfg, log)
	if err != nil {
		return err
	}
	defer func() { _ = hot.Close() }()

	playerRepo := postgres.NewPlayerRepo(store)
	cityRepo := postgres.NewCityRepo(store)
	unitRepo := postgres.NewUnitRepo(store, cfg.ChunkSize)
	movementRepo := postgres.NewMovementRepo(store)
	worldRepo := postgres.NewWorldRepo(store)
	territoryRepo := postgres.NewTerritoryRepo(store)
	bootstrapper := postgres.NewBootstrapper(store, playerRepo, cityRepo, unitRepo, territoryRepo)

	sysClock := clock.NewSystemClock()

	// ── 3. Mundo ────────────────────────────────────────────────
	gameWorld, worldState, err := loadWorld(rootCtx, worldRepo, cfg, sysClock, log)
	if err != nil {
		return err
	}

	// ── 4. Hidratación y recuperación ───────────────────────────
	units, err := unitRepo.ListAlive(rootCtx)
	if err != nil {
		return fmt.Errorf("cargar unidades: %w", err)
	}
	cities, err := cityRepo.ListAll(rootCtx)
	if err != nil {
		return fmt.Errorf("cargar ciudades: %w", err)
	}
	movements, err := movementRepo.ListActive(rootCtx)
	if err != nil {
		return fmt.Errorf("cargar movimientos activos: %w", err)
	}

	territories, controls, err := loadTerritories(rootCtx, territoryRepo, gameWorld, log)
	if err != nil {
		return err
	}

	state := simulation.NewState(gameWorld)
	state.SetTerritories(territories, controls)
	recovery := simulation.Hydrate(state, units, cities, movements, sysClock.NowMs(), log)
	log.Info("mundo rehidratado",
		"units", recovery.Units, "cities", recovery.Cities,
		"movements_resumed", recovery.Resumed,
		"movements_arrived_while_down", recovery.Arrived,
		"movements_failed", recovery.Failed)

	// Las murallas de las ciudades existentes vuelven a bloquear su tile: la capa
	// de ocupación no se persiste, se deriva del mundo.
	for _, c := range cities {
		gameWorld.SetBlocked(c.CenterX-1, c.CenterY-1, c.CenterX+1, c.CenterY+1, true)
	}

	gameStore := persistence.NewGameStore(store, unitRepo, cityRepo, movementRepo)
	if len(recovery.FinishedMovements) > 0 {
		if err := gameStore.FinishRecoveredMovements(rootCtx, recovery.FinishedMovements); err != nil {
			return fmt.Errorf("cerrar movimientos recuperados: %w", err)
		}
		log.Info("movimientos recuperados cerrados", "count", len(recovery.FinishedMovements))
	}

	// ── 5. Simulación y game loop ───────────────────────────────
	queue := persistence.NewQueue(persistence.DefaultQueueConfig(), log, metrics)
	queue.Start(rootCtx, 4)
	defer queue.Close()

	health := observability.NewHealth(5*time.Second, sysClock.Now)
	health.Register("postgres", store)
	health.Register("hot_state", hot)

	hub := ws.NewHub(metrics, log)
	commands := make(chan simulation.Command, commandQueueSize)

	sim := simulation.New(state, simulation.Deps{
		Clock:              sysClock,
		Pathfinder:         pathfinding.NewAStar(cfg.PathfindingMaxNodes, cfg.PathfindingMaxDistance),
		Broadcaster:        hub,
		Persister:          queue,
		Repos:              gameStore,
		Log:                log,
		ProtectionCooldown: cfg.CityOfflineProtectionCooldwn,
		DisconnectGrace:    cfg.PresenceTTL,
		PathMaxNodes:       cfg.PathfindingMaxNodes,
		PathMaxDistance:    cfg.PathfindingMaxDistance,
	})

	gameLoop := loop.New(sim, sysClock, commands, loop.Config{
		TickDuration:       cfg.TickDuration(),
		FlushIntervalTicks: cfg.PersistenceFlushIntervalTicks,
		MaxCommandsPerTick: 1024,
		EpochMs:            worldState.EpochMs,
		StartTick:          worldState.CurrentTick,
	}, metrics, health, log)

	// ── 6. Transporte ───────────────────────────────────────────
	authenticator := auth.NewAuthenticator(
		auth.NewVerifier(cfg.AuthJWTSecret, sysClock),
		hot.Tickets(),
	)
	presence := hot.Presence()
	dedupe := hot.Idempotency()

	wsServer := ws.NewServer(ws.Config{
		MaxMessageBytes:      cfg.WSMaxMessageBytes,
		RateLimitPerSec:      cfg.WSRateLimitPerSec,
		RateLimitBurst:       cfg.WSRateLimitBurst,
		HandshakeTimeout:     cfg.WSHandshakeTimeout,
		PingInterval:         cfg.WSPingInterval,
		ReadTimeout:          cfg.WSReadTimeout,
		WriteTimeout:         cfg.WSWriteTimeout,
		OutboundQueue:        cfg.WSOutboundQueueSize,
		InterestRadiusChunks: int(cfg.InterestRadiusChunk),
		TickDurationMs:       cfg.TickDurationMs(),
		HeartbeatIntervalMs:  cfg.PresenceHeartbeat.Milliseconds(),
		WorldWidth:           cfg.WorldWidth,
		WorldHeight:          cfg.WorldHeight,
		ChunkSize:            cfg.ChunkSize,
	}, hub, authenticator, presence, dedupe, commands, metrics, log)

	eras, err := cityRepo.ListEras(rootCtx)
	if err != nil || len(eras) == 0 {
		return fmt.Errorf("catálogo de eras vacío o ilegible: %w", err)
	}
	firstEra := eras[0]

	authAPI := httpapi.NewAuthAPI(playerRepo, cityRepo, bootstrapper,
		auth.NewIssuer(cfg.AuthJWTSecret, ticketTTL, sysClock),
		gameWorld, commands, httpapi.Options{
			DefaultEra:       firstEra.Code,
			PopulationCap:    firstEra.PopulationCap,
			InitialVillagers: 3,
			CivilizationID:   1,
			FactionID:        3, // NEUTRAL
			Territories:      territories,
			Tick:             gameLoop.Tick,
		}, log)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", health.LivenessHandler())
	mux.HandleFunc("/ready", health.ReadinessHandler())
	mux.HandleFunc("/ws", wsServer.Handler(rootCtx))
	mux.HandleFunc("/api/auth/register", withCORS(authAPI.Register()))
	mux.HandleFunc("/api/auth/login", withCORS(authAPI.Login()))

	gameHTTP := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.HandlerFor(metrics.Registry(), promhttp.HandlerOpts{}))
	metricsHTTP := &http.Server{
		Addr:              cfg.MetricsAddr,
		Handler:           metricsMux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// ── 7. Arranque ─────────────────────────────────────────────
	errCh := make(chan error, 3)

	go func() {
		log.Info("servidor de juego escuchando", "addr", cfg.HTTPAddr)
		if err := gameHTTP.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("servidor HTTP: %w", err)
		}
	}()
	go func() {
		log.Info("servidor de métricas escuchando", "addr", cfg.MetricsAddr)
		if err := metricsHTTP.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("servidor de métricas: %w", err)
		}
	}()
	go func() {
		if err := gameLoop.Run(rootCtx); err != nil && !errors.Is(err, context.Canceled) {
			errCh <- fmt.Errorf("game loop: %w", err)
		}
	}()

	// ── 8. Apagado ordenado ─────────────────────────────────────
	select {
	case <-rootCtx.Done():
		log.Info("señal de apagado recibida")
	case err := <-errCh:
		log.Error("fallo de un subsistema", "err", err)
		stop()
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	// El orden importa: primero se deja de aceptar tráfico nuevo, luego se
	// despide a quien está dentro, y sólo al final se vacían las escrituras
	// pendientes. Al revés se perderían datos.
	_ = gameHTTP.Shutdown(shutdownCtx)
	_ = metricsHTTP.Shutdown(shutdownCtx)
	hub.CloseAll(1001, "servidor en mantenimiento")

	if drained := queue.Drain(15 * time.Second); !drained {
		log.Error("la cola de persistencia no se vació a tiempo", "pending", queue.Depth())
	}
	if err := worldRepo.SaveTick(shutdownCtx, gameLoop.Tick()); err != nil {
		log.Warn("no se pudo guardar el tick final", "err", err)
	}

	log.Info("apagado completado", "tick", gameLoop.Tick())
	return nil
}

// loadWorld obtiene el mundo: lo genera la primera vez y lo verifica después.
func loadWorld(
	ctx context.Context,
	repo *postgres.WorldRepo,
	cfg config.Config,
	clk clock.Clock,
	log *slog.Logger,
) (*world.World, postgres.State, error) {
	want := postgres.State{
		Seed:      cfg.WorldSeed,
		Width:     cfg.WorldWidth,
		Height:    cfg.WorldHeight,
		ChunkSize: cfg.ChunkSize,
		EpochMs:   clk.NowMs(),
	}

	state, created, err := repo.LoadOrInit(ctx, want)
	if err != nil {
		return nil, postgres.State{}, err
	}

	// El terreno se GENERA desde la semilla, siempre. La copia persistida existe
	// para auditoría y para permitir mapas editados en el futuro, no como fuente
	// primaria: regenerar es más barato que leer 256 filas y garantiza que semilla
	// y mapa nunca divergen.
	terrain := world.Generate(state.Width, state.Height, state.Seed)
	gameWorld, err := world.New(state.Width, state.Height, state.ChunkSize, state.Seed, terrain)
	if err != nil {
		return nil, postgres.State{}, fmt.Errorf("construir el mundo: %w", err)
	}

	if created {
		if err := repo.SaveChunks(ctx, gameWorld); err != nil {
			return nil, postgres.State{}, err
		}
		log.Info("mundo generado y persistido",
			"seed", state.Seed, "chunks", int(gameWorld.ChunksPerRow())*int(gameWorld.ChunksPerColumn()))
	} else {
		log.Info("mundo existente cargado",
			"seed", state.Seed, "tick", state.CurrentTick, "epoch_ms", state.EpochMs)
	}
	return gameWorld, state, nil
}

// withCORS permite que el frontend en otro origen consuma la API de desarrollo.
func withCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

// loadTerritories siembra la geometría la primera vez y devuelve el índice y el
// control vigente.
//
// La siembra vive aquí y NO en una migración porque la geometría tiene que caber
// en el mundo, y las dimensiones del mundo son configuración
// (EO_WORLD_WIDTH / EO_WORLD_HEIGHT). Sembrar en el esquema fijaría un tamaño de
// mundo y rompería cualquier despliegue que lo cambiara.
//
// El índice es un derivado puro de la tabla: nunca se persiste y se reconstruye
// en cada arranque (INV-TERR-008).
func loadTerritories(
	ctx context.Context,
	repo *postgres.TerritoryRepo,
	w *world.World,
	log *slog.Logger,
) (*territory.Set, []territory.Control, error) {
	n, err := repo.Count(ctx)
	if err != nil {
		return nil, nil, err
	}

	if n == 0 {
		seeds, err := territory.SeedGrid(w.Width(), w.Height(), territory.DefaultSeedSize)
		if err != nil {
			return nil, nil, fmt.Errorf("generar la rejilla de territorios: %w", err)
		}
		// Toda la siembra en UNA transacción: una rejilla a medias dejaría tiles
		// sin territorio sin que nada lo indicase.
		if err := repo.SeedInTx(ctx, seeds); err != nil {
			return nil, nil, err
		}
		log.Info("territorios sembrados", "count", len(seeds), "side", territory.DefaultSeedSize)
	}

	geometry, err := repo.LoadAll(ctx)
	if err != nil {
		return nil, nil, err
	}
	controls, err := repo.LoadControls(ctx)
	if err != nil {
		return nil, nil, err
	}

	set, overlaps, err := territory.BuildSet(geometry, w.Width(), w.Height(), w.ChunkSize())
	if err != nil {
		return nil, nil, fmt.Errorf("construir el índice de territorios: %w", err)
	}

	// RN-TERR-004: un solapamiento no impide arrancar, pero deja el estado
	// marcado como inconsistente y tiene que verse en los logs.
	for _, o := range overlaps {
		log.Error("territorios solapados: INV-TERR-002 violado",
			"kept", o.Kept, "discarded", o.Discarded,
			"first_tile_x", o.X, "first_tile_y", o.Y, "tiles", o.Tiles)
	}

	log.Info("territorios cargados",
		"count", set.Len(), "controls", len(controls), "overlaps", len(overlaps))
	return set, controls, nil
}

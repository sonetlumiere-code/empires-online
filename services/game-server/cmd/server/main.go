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
	"github.com/empires-online/empires-online/services/game-server/internal/domain/safezone"
	"github.com/empires-online/empires-online/services/game-server/internal/domain/territory"
	"github.com/empires-online/empires-online/services/game-server/internal/game/loadcheck"
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
	ticketReplayTTL  = 120 * time.Second
	idempotencyTTL   = 300 * time.Second

	// Plazos de los servidores HTTP (ADR-021). Acotan lo que un socket puede
	// ocupar sin terminar de hablar: las cabeceras, la petición entera y el
	// silencio entre peticiones keep-alive. No afectan a un WebSocket ya
	// establecido: gorilla retira estos plazos del socket al hacer el upgrade y
	// desde ahí mandan WSHandshakeTimeout y WSReadTimeout.
	httpReadHeaderTimeout = 10 * time.Second
	httpReadTimeout       = 15 * time.Second
	httpIdleTimeout       = 60 * time.Second
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
		if err := postgres.Migrate(cfg.PostgresURL.Reveal(), log); err != nil {
			return fmt.Errorf("migraciones: %w", err)
		}
	} else {
		log.Info("migración automática desactivada; se asume el esquema ya migrado",
			"hint", "ejecuta el binario `migrate` antes de desplegar")
	}

	// ── 2. Dependencias con estado ──────────────────────────────
	store, err := postgres.New(rootCtx, cfg.PostgresURL.Reveal())
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
	treatyRepo := postgres.NewTreatyRepo(store)
	bootstrapper := postgres.NewBootstrapper(store, playerRepo, cityRepo, unitRepo, territoryRepo)

	sysClock := clock.NewSystemClock()

	// ── 3. Mundo ────────────────────────────────────────────────
	gameWorld, worldState, err := loadWorld(rootCtx, worldRepo, cfg, sysClock, log)
	if err != nil {
		return err
	}

	// El tick se reancla al reloj (ADR-015): world_state.current_tick sólo se
	// guarda en el apagado ordenado, y retomarlo tras una caída repetiría
	// números de tick ya emitidos. Se calcula aquí porque las reparaciones de
	// carga se auditan con él.
	startTick, clockBehind := loop.ResumeTick(worldState.CurrentTick, worldState.EpochMs,
		sysClock.NowMs(), cfg.TickDuration())
	if clockBehind {
		log.Error("invariant_violation",
			"inv_id", "INV-WORLD-004", "severity", "CRITICO", "policy", "REPAIR",
			"entity", "world_state", "tick", startTick,
			"detail", "el reloj va por detrás del último tick guardado: se arranca desde lo guardado")
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
	// Los ids de movimiento los asigna la simulación (ADR-013 §2.1): el contador
	// arranca por encima de todo lo que ya existe, terminado o no.
	maxMovementID, err := movementRepo.MaxID(rootCtx)
	if err != nil {
		return err
	}

	territories, controls, err := loadTerritories(rootCtx, territoryRepo, gameWorld, log)
	if err != nil {
		return err
	}

	eras, err := cityRepo.ListEras(rootCtx)
	if err != nil || len(eras) == 0 {
		return fmt.Errorf("catálogo de eras vacío o ilegible: %w", err)
	}

	// Validación de carga (EO-119): lo que el esquema no garantiza se comprueba
	// sobre lo recién leído, antes de construir la RAM. Un fatal aborta sin
	// escribir nada; lo reparable se corrige en una transacción auditada.
	units, controls, garrisons, err := checkLoadedState(rootCtx, postgres.NewLoadCheckRepo(store), loadcheck.Input{
		World:    gameWorld,
		EraCaps:  postgres.EraCaps(eras),
		Cities:   cities,
		Units:    units,
		Controls: controls,
		Now:      sysClock.Now(),
	}, startTick, log)
	if err != nil {
		return err
	}

	state := simulation.NewState(gameWorld)
	state.SetTerritories(territories, controls)
	state.SetMovementIDFloor(maxMovementID)
	state.SetGarrisons(garrisons)
	recovery := simulation.Hydrate(state, units, cities, movements, sysClock.NowMs(), log)
	log.Info("mundo rehidratado",
		"units", recovery.Units, "cities", recovery.Cities,
		"movements_resumed", recovery.Resumed,
		"movements_arrived_while_down", recovery.Arrived,
		"movements_failed", recovery.Failed,
		"units_reconciled", recovery.Reconciled)

	// Las murallas de las ciudades existentes vuelven a bloquear su tile: la capa
	// de ocupación no se persiste, se deriva del mundo.
	for _, c := range cities {
		minX, minY, maxX, maxY := c.UrbanBounds()
		gameWorld.SetBlocked(minX, minY, maxX, maxY, true)
	}
	// Una caída entre un alta y el volcado siguiente deja en disco unidades
	// dentro de la muralla nueva, o en marcha a través de ella: se apartan y se
	// detienen igual que en el tick del alta (RN-CITY-022).
	recovery.FinishedMovements = append(recovery.FinishedMovements,
		simulation.ClearFootprints(state, cities, sysClock.NowMs(), log)...)

	// Las zonas seguras se indexan DESPUÉS de las murallas: el índice excluye
	// los tiles ocupados (INV-SAFE-002), y construirlo antes dejaría dentro
	// tiles que ninguna unidad puede pisar. Las unidades ya hidratadas se
	// evalúan en la primera fase 5, que restablece HIDDEN desde la geometría.
	safeZones, err := loadSafeZones(rootCtx, postgres.NewSafeZoneRepo(store), gameWorld, log)
	if err != nil {
		return err
	}
	state.SetSafeZones(safeZones)

	gameStore := persistence.NewGameStore(store, unitRepo, cityRepo, movementRepo)
	if len(recovery.FinishedMovements) > 0 {
		if err := gameStore.FinishRecoveredMovements(rootCtx, recovery.FinishedMovements); err != nil {
			return fmt.Errorf("cerrar movimientos recuperados: %w", err)
		}
		log.Info("movimientos recuperados cerrados", "count", len(recovery.FinishedMovements))
	}

	// ── 5. Simulación y game loop ───────────────────────────────
	queue := persistence.NewQueue(persistence.DefaultQueueConfig(), log, metrics)
	queue.Start(rootCtx)
	// La cola se cierra al salir salvo que el loop siga vivo: cerrarla con el
	// loop encolando sería un panic (send on closed channel).
	closeQueue := true
	defer func() {
		if closeQueue {
			queue.Close()
		}
	}()

	health := observability.NewHealth(5*time.Second, sysClock.Now)
	health.Register("postgres", store)
	health.Register("hot_state", hot)

	hub := ws.NewHub(metrics, log)
	commands := make(chan simulation.Command, commandQueueSize)

	sim := simulation.New(state, simulation.Deps{
		Clock:       sysClock,
		Pathfinder:  pathfinding.NewAStar(cfg.PathfindingMaxNodes, cfg.PathfindingMaxDistance),
		Broadcaster: hub,
		Persister:   queue,
		Repos:       gameStore,
		Treaties:    treatyRepo,
		Garrisons:   postgres.NewGarrisonRepo(store, unitRepo, movementRepo, treatyRepo),
		// Las compensaciones de la cola vuelven al loop por el mismo canal que
		// los comandos de los jugadores, sin bloquear al worker (ADR-013 §2.4).
		Inbox: func(c simulation.Command) bool {
			select {
			case commands <- c:
				return true
			default:
				return false
			}
		},
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
		StartTick:          startTick,
	}, metrics, health, log)

	// ── 6. Transporte ───────────────────────────────────────────
	authenticator := auth.NewAuthenticator(
		auth.NewVerifier(cfg.AuthJWTSecret.Reveal(), sysClock),
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
		AllowedOrigins:       cfg.WSAllowedOrigins,
		MaxConnections:       cfg.WSMaxConnections,
		MaxConnectionsPerIP:  cfg.WSMaxConnectionsPerIP,
		TrustProxyHeaders:    cfg.TrustProxyHeaders,
	}, hub, authenticator, presence, dedupe, commands, metrics, log)

	// Detrás del reverse proxy del despliegue de referencia, TODAS las
	// conexiones llegan desde la IP del proxy: sin EO_TRUST_PROXY_HEADERS el
	// tope por IP sería el tope del servidor entero. No se puede validar en la
	// configuración —nada dice si hay proxy— así que se avisa donde importa.
	if cfg.IsProduction() && cfg.WSMaxConnectionsPerIP > 0 && !cfg.TrustProxyHeaders {
		log.Warn("EO_WS_MAX_CONNECTIONS_PER_IP activo sin EO_TRUST_PROXY_HEADERS: detrás de un proxy inverso todas las conexiones comparten la IP del proxy y el tope por IP sería el tope global",
			"max_connections_per_ip", cfg.WSMaxConnectionsPerIP)
	}

	firstEra := eras[0]

	// La identidad vive en apps/web (ADR-018): aquí no hay alta ni login, sólo la
	// fundación del jugador en el mundo, autorizada por un token de la audiencia
	// interna. Su jti se consume en el mismo almacén anti-replay que el ticket.
	foundingAPI := httpapi.NewFoundingAPI(playerRepo, cityRepo, bootstrapper,
		httpapi.NewFoundingAuthenticator(cfg.AuthJWTSecret.Reveal(), sysClock, hot.Tickets()),
		gameWorld, commands, httpapi.Options{
			DefaultEra:     firstEra.Code,
			CivilizationID: 1,
			FactionID:      3, // NEUTRAL
			Territories:    territories,
			Tick:           gameLoop.Tick,
			Clock:          sysClock,
		}, log)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", health.LivenessHandler())
	mux.HandleFunc("/ready", health.ReadinessHandler())
	mux.HandleFunc("/ws", wsServer.Handler(rootCtx))
	mux.HandleFunc(httpapi.FoundPath, foundingAPI.Found())
	mux.HandleFunc(httpapi.LookupPattern, foundingAPI.Lookup())

	gameHTTP := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		IdleTimeout:       httpIdleTimeout,
	}

	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.HandlerFor(metrics.Registry(), promhttp.HandlerOpts{}))
	metricsHTTP := &http.Server{
		Addr:              cfg.MetricsAddr,
		Handler:           metricsMux,
		ReadHeaderTimeout: httpReadHeaderTimeout,
		ReadTimeout:       httpReadTimeout,
		IdleTimeout:       httpIdleTimeout,
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
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
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
		// También aquí se espera al loop y se vacía la cola: un subsistema caído
		// no es motivo para perder las escrituras ya aceptadas.
		if loopStopped(loopDone, loopStopTimeout, log) {
			queue.Close()
		} else {
			closeQueue = false
		}
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

	// El loop tiene que haber parado ANTES de cerrar la cola: un Submit sobre la
	// cola cerrada sería un panic, y su volcado final es lo último que encola.
	if !loopStopped(loopDone, loopStopTimeout, log) {
		// Con el loop vivo no se puede cerrar la cola sin arriesgar un panic.
		// Lo que quede encolado se pierde: es el RPO de un apagado abrupto.
		closeQueue = false
		return nil
	}
	if drained := queue.Drain(15 * time.Second); !drained {
		log.Error("la cola de persistencia no se vació a tiempo", "pending", queue.Depth())
	}
	// Close espera además a los trabajos que un worker ya estaba ejecutando, que
	// Drain no ve porque ya salieron del canal.
	queue.Close()
	if err := worldRepo.SaveTick(shutdownCtx, gameLoop.Tick()); err != nil {
		log.Warn("no se pudo guardar el tick final", "err", err)
	}

	log.Info("apagado completado", "tick", gameLoop.Tick())
	return nil
}

// loopStopTimeout acota la espera a que el game loop termine su último tick.
// Un tick dura 100 ms; si en 5 s no ha parado, está atascado, y el apagado no
// puede quedarse colgado esperándolo.
const loopStopTimeout = 5 * time.Second

// loopStopped espera a que el game loop termine, como mucho `timeout`.
func loopStopped(done <-chan struct{}, timeout time.Duration, log *slog.Logger) bool {
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		log.Error("el game loop no se detuvo a tiempo: se sale sin vaciar la cola", "timeout", timeout)
		return false
	}
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
		// INV-WORLD-002 e INV-WORLD-005: el terreno regenerado debe ser el que
		// se persistió la primera vez. Si el generador cambió sin una migración
		// de mundo, las entidades persistidas estarían sobre otro mapa.
		if err := repo.VerifyTerrain(ctx, gameWorld); err != nil {
			log.Error("invariant_violation",
				"inv_id", "INV-WORLD-005", "severity", "ALTO", "policy", "FAIL_FAST",
				"entity", "world_chunks", "seed", state.Seed, "detail", err.Error())
			return nil, postgres.State{}, fmt.Errorf("verificar el terreno persistido: %w", err)
		}
		log.Info("mundo existente cargado y verificado",
			"seed", state.Seed, "tick", state.CurrentTick, "epoch_ms", state.EpochMs)
	}
	return gameWorld, state, nil
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
		// Señal obligatoria de docs/invariants/README.md §5.1: msg fijo e
		// inv_id, para que las violaciones se puedan filtrar en los logs.
		log.Error("invariant_violation",
			"inv_id", "INV-TERR-002", "severity", "ALTO", "policy", "REPAIR",
			"entity", fmt.Sprintf("territory:%d", o.Discarded),
			"kept", o.Kept, "discarded", o.Discarded,
			"first_tile_x", o.X, "first_tile_y", o.Y, "tiles", o.Tiles,
			"detail", "territorios solapados: gana el id menor")
	}

	log.Info("territorios cargados",
		"count", set.Len(), "controls", len(controls), "overlaps", len(overlaps))
	return set, controls, nil
}

// loadSafeZones lee `safe_zones` y construye el índice tile → zona.
//
// Ninguna anomalía de los datos impide arrancar (RN-SAFE-007): un solape se
// resuelve a favor del id menor, una zona inválida se descarta entera y una
// zona sobre una ciudad pierde los tiles amurallados. Cada caso emite la señal
// obligatoria de docs/invariants/README.md §5.1, para que el estado degradado
// sea visible y filtrable en los logs.
//
// Con la tabla vacía —el caso del mundo canónico mientras su siembra sea TBD—
// el índice existe y no contiene ninguna zona.
func loadSafeZones(
	ctx context.Context,
	repo *postgres.SafeZoneRepo,
	w *world.World,
	log *slog.Logger,
) (*safezone.Index, error) {
	zones, err := repo.LoadAll(ctx)
	if err != nil {
		return nil, err
	}

	idx, report, err := safezone.BuildIndex(zones, w)
	if err != nil {
		return nil, fmt.Errorf("construir el índice de zonas seguras: %w", err)
	}

	for _, r := range report.Rejected {
		log.Error("invariant_violation",
			"inv_id", "INV-SAFE-005", "severity", "ALTO", "policy", "FAIL_FAST",
			"entity", fmt.Sprintf("safe_zone:%d", r.ZoneID),
			"detail", "zona descartada: "+r.Reason)
	}
	for _, o := range report.Overlaps {
		log.Error("invariant_violation",
			"inv_id", "INV-SAFE-001", "severity", "ALTO", "policy", "REPAIR",
			"entity", fmt.Sprintf("safe_zone:%d", o.Discarded),
			"kept", o.Kept, "discarded", o.Discarded,
			"first_tile_x", o.X, "first_tile_y", o.Y, "tiles", o.Tiles,
			"detail", "zonas solapadas: gana el id menor")
	}
	for _, u := range report.Urban {
		log.Error("invariant_violation",
			"inv_id", "INV-SAFE-006", "severity", "MEDIO", "policy", "REPAIR",
			"entity", fmt.Sprintf("safe_zone:%d", u.ZoneID),
			"first_tile_x", u.X, "first_tile_y", u.Y, "tiles", u.Tiles,
			"detail", "la zona se solapa con una zona urbana; esos tiles quedan fuera")
	}

	log.Info("zonas seguras cargadas",
		"rows", len(zones), "indexed", idx.Len(),
		"rejected", len(report.Rejected), "overlaps", len(report.Overlaps), "urban", len(report.Urban))
	return idx, nil
}

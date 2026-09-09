package simulation

import (
	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// SnapshotResult es la respuesta a una petición de snapshot.
type SnapshotResult struct {
	Payload protocol.WorldSnapshotPayload
	Chunks  []world.ChunkCoord
	Found   bool
}

// RequestSnapshot pide el estado del área de interés de una sesión.
//
// Va por el canal de comandos, como todo lo demás, en lugar de leer el mundo
// directamente desde la goroutine de la conexión. Ese rodeo es lo que garantiza
// que NADIE fuera del game loop toque el estado: sin él, cada snapshot sería una
// carrera de datos contra la simulación.
type RequestSnapshot struct {
	PlayerID  uuid.UUID
	SessionID uuid.UUID
	// Center es el centro del área de interés. Si es nil, se centra en la ciudad
	// del jugador.
	Center *world.Tile
	// IncludeTerrain permite omitir el terreno cuando el cliente ya lo tiene:
	// el terreno es inmutable, así que basta enviarlo una vez por chunk.
	IncludeTerrain bool
	// RadiusChunks es el radio del área de interés, en chunks.
	RadiusChunks int32
	// Reply recibe el resultado. Debe tener buffer para que el loop nunca se
	// bloquee esperando a que la conexión lo lea.
	Reply chan<- SnapshotResult
}

func (RequestSnapshot) commandType() string { return "world.snapshot" }

func (s *Simulation) handleRequestSnapshot(cmd RequestSnapshot) {
	nowMs := s.deps.Clock.NowMs()
	w := s.state.World()

	center := world.Tile{X: w.Width() / 2, Y: w.Height() / 2}
	found := false

	switch {
	case cmd.Center != nil:
		center = *cmd.Center
		found = true
	default:
		if c, ok := s.state.CityOf(cmd.PlayerID); ok {
			center = c.Center()
			found = true
		}
	}

	chunks := w.ChunksInRadius(center, cmd.RadiusChunks)
	result := SnapshotResult{
		Payload: s.BuildSnapshot(chunks, nowMs, cmd.IncludeTerrain),
		Chunks:  chunks,
		Found:   found,
	}

	// Envío no bloqueante: si la conexión ya se fue, el snapshot se descarta.
	select {
	case cmd.Reply <- result:
	default:
		s.deps.Log.Debug("snapshot descartado: el solicitante ya no escucha",
			"player_id", cmd.PlayerID, "session_id", cmd.SessionID)
	}
}

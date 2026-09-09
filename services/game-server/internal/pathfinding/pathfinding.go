// Package pathfinding calcula rutas sobre el grid del mundo.
//
// El dominio depende ÚNICAMENTE de la interfaz Pathfinder. Cambiar A* plano por
// A* jerárquico más adelante no debe tocar ni el protocolo ni la lógica de unidades.
// Ver ../../../../docs/architecture/pathfinding.md
package pathfinding

import (
	"context"
	"errors"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

var (
	// ErrPathNotFound indica que no existe ninguna ruta entre origen y destino.
	ErrPathNotFound = errors.New("no existe ruta hacia el destino")
	// ErrTargetOutOfBounds indica que el destino cae fuera del mundo.
	ErrTargetOutOfBounds = errors.New("destino fuera de los límites del mundo")
	// ErrTargetNotWalkable indica que el destino no es transitable.
	ErrTargetNotWalkable = errors.New("el destino no es transitable")
	// ErrOriginNotWalkable indica que el origen no es transitable (estado corrupto).
	ErrOriginNotWalkable = errors.New("el origen no es transitable")
	// ErrPathTooLong indica que se superó el límite de distancia o de nodos explorados.
	ErrPathTooLong = errors.New("la ruta excede los límites configurados")
)

// Grid es la vista mínima del mundo que necesita el pathfinder.
//
// Deliberadamente estrecha: el algoritmo no sabe nada de unidades, ciudades ni
// jugadores. *world.World la satisface.
type Grid interface {
	Width() int32
	Height() int32
	InBounds(x, y int32) bool
	IsWalkable(x, y int32) bool
	// MoveCostUnits es el coste de ENTRAR en el tile, en décimas del coste base.
	MoveCostUnits(x, y int32) int32
	// MinMoveCostUnits es el menor coste posible de un tile transitable del mundo.
	// La heurística lo necesita para permanecer admisible.
	MinMoveCostUnits() int32
}

// Options acota una consulta concreta.
type Options struct {
	// MaxNodes limita los nodos expandidos. 0 usa el valor por defecto del pathfinder.
	MaxNodes int
	// MaxDistance limita la distancia de Chebyshev entre origen y destino, en tiles.
	MaxDistance int32
}

// Pathfinder es el contrato estable del subsistema.
type Pathfinder interface {
	// FindPath devuelve la ruta óptima como una secuencia de tiles contiguos en
	// 8-vecindad, empezando por `from` y terminando en `to`. Si from == to,
	// devuelve una ruta de un único tile.
	FindPath(ctx context.Context, grid Grid, from, to world.Tile, opts Options) ([]world.Tile, error)
}

// Escala en punto fijo de los costes internos de A*.
//
// El coste de un paso ortogonal es costUnits*costScaleOrtho y el de uno diagonal
// costUnits*costScaleDiag. Trabajar en enteros escalados, en lugar de en punto
// flotante, garantiza que dos ejecuciones idénticas produzcan exactamente la
// misma ruta en cualquier plataforma.
const (
	costScaleOrtho int32 = 1000
	// 1414/1000 ≈ √2. Truncar hacia abajo mantiene la heurística admisible.
	costScaleDiag int32 = 1414
)

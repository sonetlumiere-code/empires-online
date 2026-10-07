// Package safezone implementa las zonas seguras DENSE_FOREST y CAVERN: el
// predicado de pertenencia de un tile, el índice denso tile → zona y la regla
// que decide cuándo una unidad queda oculta.
//
// Es dominio puro: no toca base de datos, ni red, ni reloj. El índice es función
// de (zonas, terreno, capa de ocupación) y se reconstruye en cada arranque
// (INV-SAFE-007, RN-SAFE-016).
//
// Spec: ../../../../../docs/specs/safe-zones.md §6
package safezone

import (
	"fmt"
	"math"
	"sort"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/unit"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Type es el tipo de una zona. Los valores coinciden exactamente con el
// CHECK safe_zones_type_valid de la migración 000001.
type Type string

const (
	DenseForest Type = "DENSE_FOREST"
	Cavern      Type = "CAVERN"
)

// Valid indica si el tipo es uno de los dos admitidos.
func (t Type) Valid() bool { return t == DenseForest || t == Cavern }

// MaxZoneID es el mayor id que cabe en el índice denso, que guarda el id en un
// uint32 (spec §6.3). `safe_zones.id` es bigint, así que el esquema no puede
// expresar el límite y se comprueba al construir.
const MaxZoneID = math.MaxUint32

// Zone es una fila de `safe_zones`. El rectángulo es una caja delimitadora
// INCLUSIVA (RN-SAFE-003); la zona real es su intersección con el predicado de
// terreno de su tipo (RN-SAFE-001, RN-SAFE-002).
type Zone struct {
	ID                     int64
	Name                   string
	Type                   Type
	MinX, MinY, MaxX, MaxY int32
}

// Contains indica si el tile cae dentro de la caja. No dice si pertenece a la
// zona: para eso hace falta además el predicado de terreno.
func (z Zone) Contains(x, y int32) bool {
	return x >= z.MinX && x <= z.MaxX && y >= z.MinY && y <= z.MaxY
}

// Grid es lo que el índice necesita saber del mundo. *world.World lo cumple.
type Grid interface {
	Width() int32
	Height() int32
	TerrainAt(x, y int32) world.TerrainType
	IsBlocked(x, y int32) bool
}

// satisfiesTerrain aplica el predicado de terreno del tipo de zona, sin mirar la
// capa de ocupación.
//
//   - DENSE_FOREST: el tile es FOREST (RN-SAFE-001).
//   - CAVERN: el tile es transitable y tiene al menos un vecino MOUNTAIN en las
//     8 direcciones (RN-SAFE-002). Fuera del mundo TerrainAt devuelve WATER, así
//     que el borde nunca cuenta como montaña.
//
// Ningún tile MOUNTAIN ni WATER puede satisfacer ninguno de los dos
// (RN-SAFE-004).
func satisfiesTerrain(g Grid, t Type, x, y int32) bool {
	terrain := g.TerrainAt(x, y)
	switch t {
	case DenseForest:
		return terrain == world.Forest
	case Cavern:
		if !terrain.Walkable() {
			return false
		}
		for dy := int32(-1); dy <= 1; dy++ {
			for dx := int32(-1); dx <= 1; dx++ {
				if dx == 0 && dy == 0 {
					continue
				}
				if g.TerrainAt(x+dx, y+dy) == world.Mountain {
					return true
				}
			}
		}
	}
	return false
}

// Overlap describe dos zonas que reclaman el mismo tile (INV-SAFE-001).
type Overlap struct {
	// Kept es la zona que se queda el tile: siempre la de id menor (RN-SAFE-007).
	Kept      int64
	Discarded int64
	// X, Y es el primer tile en conflicto en orden de barrido fila-mayor.
	X, Y  int32
	Tiles int
}

// Rejection es una zona que no se incorpora al índice (INV-SAFE-005, política
// FAIL_FAST de la carga de esa zona).
type Rejection struct {
	ZoneID int64
	Reason string
}

// UrbanOverlap es una zona cuyo predicado se cumple sobre tiles ocupados por
// una construcción —en el MVP, la muralla de una ciudad—. Esos tiles quedan
// fuera del índice (INV-SAFE-002) y el solape se informa (INV-SAFE-006).
type UrbanOverlap struct {
	ZoneID int64
	X, Y   int32
	Tiles  int
}

// Report es lo que la construcción encontró y no impidió construir.
type Report struct {
	Overlaps []Overlap
	Rejected []Rejection
	Urban    []UrbanOverlap
}

// Index es el índice denso tile → zona de spec §6.3.
type Index struct {
	width, height int32
	// tiles[y*width+x] = safe_zones.id, o 0 si el tile no pertenece a ninguna.
	tiles   []uint32
	byID    map[int64]Zone
	ordered []Zone
}

// BuildIndex construye el índice.
//
// Las zonas se pintan en orden ascendente de id y cada tile se escribe una sola
// vez: una segunda escritura sobre un tile ya asignado ES la detección del
// solape, y como el primero en pintar es el id menor, basta con no sobreescribir
// para que gane (RN-SAFE-006, RN-SAFE-007).
//
// Un tile entra en el índice sólo si satisface el predicado de su tipo y no está
// ocupado por una construcción. Eso hace que todo tile del índice sea
// transitable (INV-SAFE-002) y que una zona sobre una ciudad quede recortada en
// vez de rota (INV-SAFE-006).
//
// Nada de lo anterior impide arrancar: un error sólo significa que el mundo no
// tiene dimensiones utilizables.
func BuildIndex(zones []Zone, g Grid) (*Index, Report, error) {
	width, height := g.Width(), g.Height()
	if width <= 0 || height <= 0 {
		return nil, Report{}, fmt.Errorf("dimensiones de mundo inválidas: %d × %d", width, height)
	}

	idx := &Index{
		width:  width,
		height: height,
		tiles:  make([]uint32, int(width)*int(height)),
		byID:   make(map[int64]Zone, len(zones)),
	}
	var report Report

	// Copia propia antes de ordenar: no se reordena el slice del llamante.
	orden := make([]Zone, len(zones))
	copy(orden, zones)
	sort.Slice(orden, func(i, j int) bool { return orden[i].ID < orden[j].ID })

	conflictos := make(map[[2]int64]*Overlap)
	for _, z := range orden {
		if reason := validate(z, width, height); reason != "" {
			report.Rejected = append(report.Rejected, Rejection{ZoneID: z.ID, Reason: reason})
			continue
		}
		if _, dup := idx.byID[z.ID]; dup {
			report.Rejected = append(report.Rejected, Rejection{ZoneID: z.ID, Reason: "id duplicado"})
			continue
		}
		idx.byID[z.ID] = z
		idx.ordered = append(idx.ordered, z)

		var urban *UrbanOverlap
		id := uint32(z.ID)
		for y := z.MinY; y <= z.MaxY; y++ {
			fila := int(y) * int(width)
			for x := z.MinX; x <= z.MaxX; x++ {
				if !satisfiesTerrain(g, z.Type, x, y) {
					continue
				}
				if g.IsBlocked(x, y) {
					if urban == nil {
						urban = &UrbanOverlap{ZoneID: z.ID, X: x, Y: y}
					}
					urban.Tiles++
					continue
				}
				pos := fila + int(x)
				if previo := idx.tiles[pos]; previo != 0 {
					clave := [2]int64{int64(previo), z.ID}
					if c, ok := conflictos[clave]; ok {
						c.Tiles++
					} else {
						conflictos[clave] = &Overlap{Kept: int64(previo), Discarded: z.ID, X: x, Y: y, Tiles: 1}
					}
					continue
				}
				idx.tiles[pos] = id
			}
		}
		if urban != nil {
			report.Urban = append(report.Urban, *urban)
		}
	}

	report.Overlaps = ordenarSolapes(conflictos)
	return idx, report, nil
}

// validate comprueba INV-SAFE-005 y el rango del índice. Devuelve el motivo del
// rechazo, o "" si la zona es válida.
func validate(z Zone, width, height int32) string {
	switch {
	case z.ID < 1 || z.ID > MaxZoneID:
		return fmt.Sprintf("id %d fuera de [1, %d]", z.ID, int64(MaxZoneID))
	case !z.Type.Valid():
		return fmt.Sprintf("zone_type %q desconocido", z.Type)
	case z.MinX > z.MaxX || z.MinY > z.MaxY:
		return fmt.Sprintf("límites desordenados (%d,%d)-(%d,%d)", z.MinX, z.MinY, z.MaxX, z.MaxY)
	case z.MinX < 0 || z.MinY < 0 || z.MaxX >= width || z.MaxY >= height:
		// La contención no puede ser un CHECK: las dimensiones del mundo son
		// configuración y la migración no las conoce.
		return fmt.Sprintf("(%d,%d)-(%d,%d) se sale del mundo %d × %d",
			z.MinX, z.MinY, z.MaxX, z.MaxY, width, height)
	}
	return ""
}

func ordenarSolapes(m map[[2]int64]*Overlap) []Overlap {
	if len(m) == 0 {
		return nil
	}
	out := make([]Overlap, 0, len(m))
	for _, v := range m {
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kept != out[j].Kept {
			return out[i].Kept < out[j].Kept
		}
		return out[i].Discarded < out[j].Discarded
	})
	return out
}

// ZoneAt resuelve la zona que contiene el tile.
//
// Fuera del mundo devuelve "sin zona" y nunca entra en pánico (RN-SAFE-005). Un
// índice nil —servidor sin zonas cargadas— se comporta como uno vacío.
func (i *Index) ZoneAt(x, y int32) (Zone, bool) {
	if i == nil || x < 0 || y < 0 || x >= i.width || y >= i.height {
		return Zone{}, false
	}
	id := i.tiles[int(y)*int(i.width)+int(x)]
	if id == 0 {
		return Zone{}, false
	}
	z, ok := i.byID[int64(id)]
	return z, ok
}

// Len es el número de zonas incorporadas al índice.
func (i *Index) Len() int {
	if i == nil {
		return 0
	}
	return len(i.ordered)
}

// TileCount es cuántos tiles pertenecen a la zona tras aplicar el predicado.
func (i *Index) TileCount(id int64) int {
	if i == nil {
		return 0
	}
	n := 0
	for _, v := range i.tiles {
		if int64(v) == id {
			n++
		}
	}
	return n
}

// Tiles devuelve una copia del índice crudo, fila-mayor. Existe para que el
// test de determinismo pueda comparar dos construcciones byte a byte
// (INV-SAFE-007).
func (i *Index) Tiles() []uint32 {
	if i == nil {
		return nil
	}
	out := make([]uint32, len(i.tiles))
	copy(out, i.tiles)
	return out
}

// Exclude retira del índice los tiles de un rectángulo. Lo usa la fundación de
// una ciudad: su muralla pasa a estar ocupada, y un tile ocupado no puede
// seguir siendo refugio (INV-SAFE-002).
//
// Devuelve, por zona y en orden ascendente de id, cuántos tiles perdió.
func (i *Index) Exclude(minX, minY, maxX, maxY int32) []UrbanOverlap {
	if i == nil {
		return nil
	}
	perdidos := make(map[int64]*UrbanOverlap)
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			if x < 0 || y < 0 || x >= i.width || y >= i.height {
				continue
			}
			pos := int(y)*int(i.width) + int(x)
			id := int64(i.tiles[pos])
			if id == 0 {
				continue
			}
			i.tiles[pos] = 0
			if p, ok := perdidos[id]; ok {
				p.Tiles++
			} else {
				perdidos[id] = &UrbanOverlap{ZoneID: id, X: x, Y: y, Tiles: 1}
			}
		}
	}
	out := make([]UrbanOverlap, 0, len(perdidos))
	for _, p := range perdidos {
		out = append(out, *p)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ZoneID < out[b].ZoneID })
	return out
}

// NextStatus decide el estado de una unidad tras evaluar el ocultamiento en la
// fase 5 del tick (spec §6.4 y §7).
//
//   - IDLE, viva, sin movimiento y dentro de una zona → HIDDEN (RN-SAFE-010).
//   - HIDDEN fuera de toda zona → IDLE (transición T8: la zona dejó de
//     existir, o el estado venía de la base y ya no se cumple).
//   - HIDDEN con un movimiento vivo → MOVING. Es un estado que INV-SAFE-003
//     prohíbe; se repara hacia el estado que su movimiento implica
//     (INV-UNIT-005) en lugar de a IDLE, que mentiría sobre el movimiento.
//   - MOVING, GARRISONED y DEAD no se tocan nunca: MOVING → HIDDEN y
//     GARRISONED → HIDDEN están prohibidas.
func NextStatus(status unit.Status, hp int32, moving, inZone bool) unit.Status {
	switch status {
	case unit.StatusIdle:
		if hp > 0 && !moving && inZone {
			return unit.StatusHidden
		}
	case unit.StatusHidden:
		switch {
		case moving:
			return unit.StatusMoving
		case !inZone || hp <= 0:
			return unit.StatusIdle
		}
	}
	return status
}

// Member indica si un tile pertenecería a una zona de ese tipo: cumple el
// predicado de terreno y no está ocupado por una construcción. Es la misma
// condición que aplica BuildIndex, expuesta para quien necesita evaluar un
// tile suelto sin construir un índice de 1 MiB —la siembra de desarrollo, por
// ejemplo—.
func Member(g Grid, t Type, x, y int32) bool {
	return satisfiesTerrain(g, t, x, y) && !g.IsBlocked(x, y)
}

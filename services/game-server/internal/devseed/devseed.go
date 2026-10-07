// Package devseed decide qué sembrar en una base de DESARROLLO para poder
// probar a mano el juego con varios jugadores y con safe zones visibles.
//
// Es lógica pura —no toca la base ni la red— para que la colocación sea
// testeable y reproducible: el mismo mundo y las mismas ciudades producen
// siempre las mismas zonas. La escritura la hace cmd/devseed.
//
// Nada de esto se ejecuta en producción ni forma parte del mundo canónico: la
// siembra de zonas del mundo canónico es una decisión de diseño pendiente
// (docs/specs/safe-zones.md §2). Ver docs/roadmap/backlog.md, EO-114.
package devseed

import (
	"fmt"

	"github.com/empires-online/empires-online/services/game-server/internal/domain/safezone"
	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// Players son los jugadores de desarrollo. Se crean todos desde la misma
// semilla de emplazamiento (AnchorSeed), así que la búsqueda en espiral los
// coloca juntos, a la separación mínima entre ciudades: caben en la misma área
// de interés y se ven entre sí, que es lo que hace falta para probar a mano la
// visibilidad de las unidades ocultas.
var Players = []string{"dev_norte", "dev_sur", "dev_este", "dev_oeste"}

// AnchorSeed es el punto de partida común de la búsqueda de emplazamiento.
const AnchorSeed uint64 = 0x5eed_0000_0000_0114

// Password es la contraseña de las cuentas de desarrollo. No es un secreto: es
// un valor de prueba para una base local, del mismo tipo que la contraseña de
// PostgreSQL de .env.example. cmd/devseed se niega a ejecutarse fuera de
// EO_ENV=development.
const Password = "semilla-de-desarrollo"

// Parámetros de colocación de zonas.
const (
	// ZoneHalfSide da cajas de 7 × 7 tiles.
	ZoneHalfSide int32 = 3
	// MinZoneTiles es el mínimo de tiles útiles para que merezca la pena una
	// zona: con menos, encontrar dónde detenerse sería una lotería.
	MinZoneTiles = 10
	// MinRing y MaxRing acotan la distancia, en tiles, entre el centro de la
	// ciudad y el de la zona. Lo bastante lejos para no rozar la muralla, lo
	// bastante cerca para estar dentro del área de interés de la ciudad.
	MinRing int32 = 6
	MaxRing int32 = 48
	// CityClearance es el radio alrededor del centro de una ciudad que ninguna
	// zona puede tocar: la muralla más un margen (INV-SAFE-006).
	CityClearance int32 = 3
)

// Rect es un rectángulo inclusivo.
type Rect struct{ MinX, MinY, MaxX, MaxY int32 }

func (r Rect) intersects(o Rect) bool {
	return r.MinX <= o.MaxX && o.MinX <= r.MaxX && r.MinY <= o.MaxY && o.MinY <= r.MaxY
}

// CityRect es el área reservada alrededor del centro de una ciudad.
func CityRect(center world.Tile) Rect {
	return Rect{
		MinX: center.X - CityClearance, MinY: center.Y - CityClearance,
		MaxX: center.X + CityClearance, MaxY: center.Y + CityClearance,
	}
}

// ZoneRect es el rectángulo de una zona.
func ZoneRect(z safezone.Zone) Rect {
	return Rect{MinX: z.MinX, MinY: z.MinY, MaxX: z.MaxX, MaxY: z.MaxY}
}

// ZoneName es el nombre de la zona de desarrollo de un tipo junto a una ciudad.
// Es también la clave de idempotencia: cmd/devseed no siembra dos veces una
// zona con el mismo nombre.
func ZoneName(t safezone.Type, owner string) string {
	return fmt.Sprintf("[dev] %s junto a %s", t, owner)
}

// PlaceZone busca, en espiral alrededor de `near`, la primera caja de 7 × 7 que
// cabe en el mundo, no toca ninguno de los rectángulos ocupados y contiene al
// menos MinZoneTiles tiles que pertenecerían a una zona de ese tipo.
//
// El recorrido es determinista: anillos crecientes y, dentro de cada anillo,
// un orden fijo. Devuelve false si no hay sitio dentro de MaxRing.
func PlaceZone(w *world.World, near world.Tile, t safezone.Type, taken []Rect) (safezone.Zone, int, bool) {
	for ring := MinRing; ring <= MaxRing; ring++ {
		for _, c := range ring8(near, ring) {
			box := Rect{
				MinX: c.X - ZoneHalfSide, MinY: c.Y - ZoneHalfSide,
				MaxX: c.X + ZoneHalfSide, MaxY: c.Y + ZoneHalfSide,
			}
			if box.MinX < 0 || box.MinY < 0 || box.MaxX >= w.Width() || box.MaxY >= w.Height() {
				continue
			}
			if collides(box, taken) {
				continue
			}
			n := 0
			for y := box.MinY; y <= box.MaxY; y++ {
				for x := box.MinX; x <= box.MaxX; x++ {
					if safezone.Member(w, t, x, y) {
						n++
					}
				}
			}
			if n >= MinZoneTiles {
				return safezone.Zone{
					Type: t, MinX: box.MinX, MinY: box.MinY, MaxX: box.MaxX, MaxY: box.MaxY,
				}, n, true
			}
		}
	}
	return safezone.Zone{}, 0, false
}

func collides(r Rect, taken []Rect) bool {
	for _, o := range taken {
		if r.intersects(o) {
			return true
		}
	}
	return false
}

// ring8 devuelve los tiles a distancia de Chebyshev exacta `r`, en un orden
// fijo: fila superior, filas intermedias por los dos lados, fila inferior.
func ring8(c world.Tile, r int32) []world.Tile {
	out := make([]world.Tile, 0, 8*r)
	for x := c.X - r; x <= c.X+r; x++ {
		out = append(out, world.Tile{X: x, Y: c.Y - r})
	}
	for y := c.Y - r + 1; y <= c.Y+r-1; y++ {
		out = append(out, world.Tile{X: c.X - r, Y: y}, world.Tile{X: c.X + r, Y: y})
	}
	for x := c.X - r; x <= c.X+r; x++ {
		out = append(out, world.Tile{X: x, Y: c.Y + r})
	}
	return out
}

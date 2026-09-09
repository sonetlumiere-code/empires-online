package world

// Generación determinista del mundo a partir de una semilla.
//
// Invariante INV-WORLD-005: la misma semilla y las mismas dimensiones producen
// SIEMPRE exactamente el mismo mapa, byte a byte, en cualquier máquina. Por eso
// el ruido se construye sobre un hash entero (splitmix64) y no sobre math/rand,
// y por eso la interpolación usa aritmética entera en lugar de punto flotante.

// Generate produce el terreno de un mundo de width x height a partir de una semilla.
//
// El resultado es un byte por tile, en orden fila-mayor. El algoritmo combina dos
// campos de ruido de valor (elevación y humedad) con varias octavas, y traza
// después un puñado de caminos que atraviesan el mapa.
func Generate(width, height int32, seed uint64) []byte {
	terrain := make([]byte, int(width)*int(height))

	elevation := valueNoise2D(width, height, seed^0xA24BAED4963EE407, 6, 4)
	moisture := valueNoise2D(width, height, seed^0x9E3779B97F4A7C15, 4, 3)

	for y := int32(0); y < height; y++ {
		for x := int32(0); x < width; x++ {
			i := int(y)*int(width) + int(x)
			e := elevation[i] // 0..65535
			m := moisture[i]  // 0..65535

			switch {
			case e > 58000:
				terrain[i] = byte(Mountain)
			case e > 48000:
				terrain[i] = byte(Hill)
			case e < 12000:
				terrain[i] = byte(Water)
			case m > 42000:
				terrain[i] = byte(Forest)
			default:
				terrain[i] = byte(Grassland)
			}
		}
	}

	carveRoads(terrain, width, height, seed)
	return terrain
}

// carveRoads traza caminos rectos entre bordes opuestos. Los caminos sólo se
// dibujan sobre terreno transitable: nunca crean puentes sobre agua ni túneles
// bajo montañas, porque eso rompería la coherencia visual del mapa.
func carveRoads(terrain []byte, width, height int32, seed uint64) {
	roads := 4
	for r := 0; r < roads; r++ {
		h := splitmix64(seed ^ (uint64(r+1) * 0x2545F4914F6CDD1D))
		if r%2 == 0 {
			y := int32(h % uint64(height))
			for x := int32(0); x < width; x++ {
				paintRoad(terrain, width, height, x, y+int32((h>>32)%3)-1)
			}
		} else {
			x := int32(h % uint64(width))
			for y := int32(0); y < height; y++ {
				paintRoad(terrain, width, height, x+int32((h>>32)%3)-1, y)
			}
		}
	}
}

func paintRoad(terrain []byte, width, height, x, y int32) {
	if x < 0 || y < 0 || x >= width || y >= height {
		return
	}
	i := int(y)*int(width) + int(x)
	if TerrainType(terrain[i]).Walkable() {
		terrain[i] = byte(Road)
	}
}

// valueNoise2D genera un campo de ruido de valor con varias octavas.
// Devuelve valores en [0, 65535]. Todo el cálculo es entero y determinista.
func valueNoise2D(width, height int32, seed uint64, baseCells int32, octaves int) []uint16 {
	acc := make([]int64, int(width)*int(height))
	var totalWeight int64

	cells := baseCells
	weight := int64(1 << octaves)

	for o := 0; o < octaves; o++ {
		octaveSeed := seed ^ (uint64(o+1) * 0xD1B54A32D192ED03)
		addOctave(acc, width, height, cells, octaveSeed, weight)
		totalWeight += weight
		cells *= 2
		weight /= 2
		if weight == 0 {
			weight = 1
		}
	}

	out := make([]uint16, len(acc))
	for i, v := range acc {
		n := v / totalWeight
		if n < 0 {
			n = 0
		}
		if n > 65535 {
			n = 65535
		}
		out[i] = uint16(n)
	}
	return out
}

// addOctave suma una octava de ruido de valor con interpolación bilineal suavizada.
func addOctave(acc []int64, width, height, cells int32, seed uint64, weight int64) {
	if cells < 1 {
		cells = 1
	}
	// Tamaño de celda en tiles (al menos 1).
	cellW := width / cells
	cellH := height / cells
	if cellW < 1 {
		cellW = 1
	}
	if cellH < 1 {
		cellH = 1
	}

	for y := int32(0); y < height; y++ {
		gy := y / cellH
		fy := y % cellH
		ty := smoothstepFixed(int64(fy), int64(cellH))

		for x := int32(0); x < width; x++ {
			gx := x / cellW
			fx := x % cellW
			tx := smoothstepFixed(int64(fx), int64(cellW))

			v00 := int64(latticeValue(seed, gx, gy))
			v10 := int64(latticeValue(seed, gx+1, gy))
			v01 := int64(latticeValue(seed, gx, gy+1))
			v11 := int64(latticeValue(seed, gx+1, gy+1))

			// Interpolación bilineal en punto fijo con denominador 1<<16.
			top := v00 + ((v10-v00)*tx)>>16
			bot := v01 + ((v11-v01)*tx)>>16
			val := top + ((bot-top)*ty)>>16

			acc[int(y)*int(width)+int(x)] += val * weight
		}
	}
}

// smoothstepFixed devuelve 3t²-2t³ en punto fijo con denominador 1<<16,
// para t = num/den en [0,1). Suaviza las transiciones entre celdas del retículo.
func smoothstepFixed(num, den int64) int64 {
	if den <= 0 {
		return 0
	}
	t := (num << 16) / den // t en [0, 65536)
	t2 := (t * t) >> 16
	t3 := (t2 * t) >> 16
	return 3*t2 - 2*t3
}

// latticeValue devuelve el valor pseudoaleatorio [0,65535] del vértice del retículo.
func latticeValue(seed uint64, gx, gy int32) uint16 {
	h := splitmix64(seed ^ (uint64(uint32(gx)) * 0x9E3779B97F4A7C15) ^ (uint64(uint32(gy)) * 0xC2B2AE3D27D4EB4F))
	return uint16(h >> 48)
}

// splitmix64 es un mezclador entero de alta calidad y totalmente determinista.
func splitmix64(x uint64) uint64 {
	x += 0x9E3779B97F4A7C15
	z := x
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

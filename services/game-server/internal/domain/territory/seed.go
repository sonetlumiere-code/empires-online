package territory

import "fmt"

// DefaultSeedSize es el lado, en tiles, de cada territorio sembrado.
//
// Con el mundo de 512 × 512 del MVP da una rejilla de 8 × 8 = 64 territorios,
// cada uno de 64 × 64 tiles: cuatro chunks de lado con el EO_CHUNK_SIZE por
// defecto. Es lo bastante grande para que fundar dentro de uno signifique algo
// y lo bastante pequeño para que el mundo tenga más de un dueño posible.
const DefaultSeedSize = 64

// SeedGrid divide el mundo entero en una rejilla de territorios rectangulares.
//
// Devuelve los territorios SIN id: los asigna la base de datos, que es quien los
// genera (`GENERATED ALWAYS AS IDENTITY`). El orden del slice es el orden de
// inserción, y por tanto el orden en que la identidad reparte los ids: recorrido
// por filas, de arriba abajo y de izquierda a derecha. Esa correspondencia es lo
// que hace que sembrar dos veces el mismo mundo produzca la misma geometría con
// los mismos ids.
//
// La rejilla cubre el mundo COMPLETO sin huecos ni solapamientos. Cuando el lado
// no divide exacto, la última columna y la última fila son más estrechas en
// lugar de desbordar: un territorio fuera del mundo violaría INV-TERR-001 y
// haría que el servidor no arrancase.
//
// Por qué se siembra desde Go y no desde una migración: la geometría tiene que
// caber en el mundo, y las dimensiones del mundo son configuración
// (EO_WORLD_WIDTH / EO_WORLD_HEIGHT). Una migración no las conoce, así que
// sembrar allí significaría fijar un tamaño de mundo en el esquema y romper
// cualquier despliegue que lo cambiara.
func SeedGrid(width, height, tileSize int32) ([]Territory, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("dimensiones de mundo inválidas: %d × %d", width, height)
	}
	if tileSize <= 0 {
		return nil, fmt.Errorf("lado de territorio inválido: %d", tileSize)
	}

	columnas := (width + tileSize - 1) / tileSize
	filas := (height + tileSize - 1) / tileSize

	// El índice denso guarda el id en uint16. Comprobarlo aquí evita sembrar una
	// rejilla que después no se podría indexar, que es un fallo mucho más caro de
	// diagnosticar: ocurriría en el arranque siguiente y lejos de su causa.
	total := int64(columnas) * int64(filas)
	if total > MaxTerritories {
		return nil, fmt.Errorf(
			"la rejilla daría %d territorios y el índice admite %d: usa un lado mayor que %d",
			total, MaxTerritories, tileSize,
		)
	}

	out := make([]Territory, 0, total)
	for fy := int32(0); fy < filas; fy++ {
		for fx := int32(0); fx < columnas; fx++ {
			minX, minY := fx*tileSize, fy*tileSize
			out = append(out, Territory{
				Name: fmt.Sprintf("Región %d-%d", fx, fy),
				MinX: minX,
				MinY: minY,
				MaxX: min32(minX+tileSize-1, width-1),
				MaxY: min32(minY+tileSize-1, height-1),
			})
		}
	}
	return out, nil
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

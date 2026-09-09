// Package territory implementa la pertenencia de un tile a un territorio, la
// huella de chunks de cada territorio y la validación del control vigente.
//
// Es dominio puro: no toca base de datos, ni red, ni reloj. Todo lo que hay
// aquí es función de sus argumentos, lo que permite que el índice se reconstruya
// en cada arranque y dé exactamente el mismo resultado (INV-TERR-008).
//
// Spec: ../../../../../docs/specs/territory.md §6.3
package territory

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/empires-online/empires-online/services/game-server/internal/game/world"
)

// MaxTerritories es el límite que impone el índice denso.
//
// El índice guarda el `territories.id` directamente en un uint16 para que la
// resolución tile → territorio sea una sola lectura de slice. Como en base de
// datos el id es `bigint`, la restricción no la puede expresar el esquema y se
// comprueba al construir el índice (spec §6.3). 65535 territorios queda
// holgadamente por encima de cualquier escenario previsible en un mundo de
// 512 × 512.
const MaxTerritories = 65535

var (
	// ErrRectanguloInvalido lo devuelve la construcción cuando un territorio no
	// cumple INV-TERR-001.
	ErrRectanguloInvalido = errors.New("rectángulo de territorio inválido")
	// ErrIDFueraDeRango lo devuelve la construcción cuando un id no cabe en el
	// índice denso.
	ErrIDFueraDeRango = errors.New("id de territorio fuera del rango del índice")
	// ErrControlInconsistente lo devuelve Validate cuando el control viola
	// INV-TERR-004 o INV-TERR-007.
	ErrControlInconsistente = errors.New("control de territorio inconsistente")
)

// OwnerType es el tipo de dueño de un territorio. Los valores coinciden
// exactamente con el CHECK territory_control_owner_type_valid y con el enum del
// protocolo: son la misma cadena en los tres sitios, a propósito.
type OwnerType string

const (
	OwnerNone    OwnerType = "NONE"
	OwnerPlayer  OwnerType = "PLAYER"
	OwnerClan    OwnerType = "CLAN"
	OwnerFaction OwnerType = "FACTION"
)

// Valid indica si el valor es uno de los cuatro admitidos.
func (o OwnerType) Valid() bool {
	switch o {
	case OwnerNone, OwnerPlayer, OwnerClan, OwnerFaction:
		return true
	}
	return false
}

// Territory es la geometría de un territorio: estable, inmutable en runtime.
// Quién lo controla vive aparte, en Control.
type Territory struct {
	ID                     int64
	Name                   string
	MinX, MinY, MaxX, MaxY int32
}

// Contains indica si el tile pertenece al territorio.
//
// Los cuatro límites son INCLUSIVOS (RN-TERR-001). Es la regla que más veces se
// escribe mal, y por eso el test la ejercita en los cuatro bordes y las cuatro
// esquinas por separado.
func (t Territory) Contains(x, y int32) bool {
	return x >= t.MinX && x <= t.MaxX && y >= t.MinY && y <= t.MaxY
}

// Control es el estado de control vigente de un territorio.
type Control struct {
	TerritoryID   int64
	OwnerType     OwnerType
	OwnerID       *string
	ControlPoints int32
	Contested     bool
	CapturedAt    *time.Time
	Version       int32
}

// Validate comprueba los invariantes de control que el esquema no puede
// garantizar por sí solo.
//
// INV-TERR-004 sí tiene CHECK en la migración, y aun así se comprueba aquí: el
// CHECK protege la base de datos, esto protege de construir en memoria un
// estado que la base rechazaría más tarde, cuando el origen del error ya no sea
// evidente. INV-TERR-007 no tiene CHECK y sólo se garantiza aquí.
func (c Control) Validate() error {
	if !c.OwnerType.Valid() {
		return fmt.Errorf("%w: owner_type %q desconocido", ErrControlInconsistente, c.OwnerType)
	}

	// INV-TERR-004: (owner_type = 'NONE') = (owner_id IS NULL).
	sinDueño := c.OwnerType == OwnerNone
	sinID := c.OwnerID == nil
	if sinDueño != sinID {
		return fmt.Errorf(
			"%w: owner_type=%s exige owner_id %s (INV-TERR-004)",
			ErrControlInconsistente, c.OwnerType,
			map[bool]string{true: "nulo", false: "no nulo"}[sinDueño],
		)
	}

	// INV-TERR-007: un dueño distinto de NONE exige captured_at.
	if !sinDueño && c.CapturedAt == nil {
		return fmt.Errorf(
			"%w: owner_type=%s exige captured_at (INV-TERR-007)",
			ErrControlInconsistente, c.OwnerType,
		)
	}

	if c.ControlPoints < 0 {
		return fmt.Errorf("%w: control_points negativo (%d)", ErrControlInconsistente, c.ControlPoints)
	}
	return nil
}

// Overlap describe dos territorios que comparten al menos un tile, lo cual
// viola INV-TERR-002.
//
// No es un error de construcción: el servidor arranca igualmente con el estado
// marcado como inconsistente (RN-TERR-004). Devolverlo en lugar de abortar es
// deliberado — negarse a arrancar por datos sembrados mal deja el mundo
// inaccesible, y el modo degradado es preferible mientras la resolución sea
// determinista.
type Overlap struct {
	// Kept es el territorio que gana el tile: siempre el de id menor.
	Kept int64
	// Discarded es el que lo pierde.
	Discarded int64
	// X, Y es el PRIMER tile en conflicto, en orden de barrido. Basta uno para
	// diagnosticar; registrar todos los tiles de un solapamiento grande llenaría
	// el log sin añadir información.
	X, Y int32
	// Tiles es cuántos tiles comparten en total, que sí dice cómo de grave es.
	Tiles int
}

// Set es el conjunto de territorios del mundo con su índice de resolución.
//
// Es un derivado puro de la tabla `territories`: nunca se persiste y se
// reconstruye en cada arranque (spec §10).
type Set struct {
	width, height int32
	chunkSize     int32

	// tiles[y*width+x] = territories.id, o 0 si el tile no pertenece a ninguno.
	// Se elige tabla densa frente a recorrer los rectángulos porque la consulta
	// ocurre dentro del tick y debe costar lo mismo con 3 territorios que con
	// 3000.
	tiles []uint16

	// ordered va en orden ascendente de id. Toda iteración usa este slice y
	// nunca un map: el orden de iteración de un map de Go es aleatorio por
	// diseño y arruinaría el determinismo (canon §8).
	ordered []Territory

	byID       map[int64]Territory
	footprints map[int64][]world.ChunkCoord
}

// BuildSet construye el conjunto y su índice.
//
// Devuelve los solapamientos encontrados para que el llamante los registre y
// los cuente; un solapamiento no impide construir. Un error sí: significa que
// los datos no se pueden representar en absoluto.
func BuildSet(territories []Territory, width, height, chunkSize int32) (*Set, []Overlap, error) {
	if width <= 0 || height <= 0 {
		return nil, nil, fmt.Errorf("dimensiones de mundo inválidas: %d × %d", width, height)
	}
	if chunkSize <= 0 {
		return nil, nil, fmt.Errorf("tamaño de chunk inválido: %d", chunkSize)
	}

	s := &Set{
		width:      width,
		height:     height,
		chunkSize:  chunkSize,
		tiles:      make([]uint16, int(width)*int(height)),
		ordered:    make([]Territory, 0, len(territories)),
		byID:       make(map[int64]Territory, len(territories)),
		footprints: make(map[int64][]world.ChunkCoord, len(territories)),
	}

	// Copia propia antes de ordenar: BuildSet no debe reordenar el slice del
	// llamante como efecto colateral.
	orden := make([]Territory, len(territories))
	copy(orden, territories)
	sort.Slice(orden, func(i, j int) bool { return orden[i].ID < orden[j].ID })

	for _, t := range orden {
		if err := validarRectangulo(t, width, height); err != nil {
			return nil, nil, err
		}
		if _, existe := s.byID[t.ID]; existe {
			return nil, nil, fmt.Errorf("%w: id %d duplicado", ErrRectanguloInvalido, t.ID)
		}
		s.ordered = append(s.ordered, t)
		s.byID[t.ID] = t
		s.footprints[t.ID] = ChunkFootprint(t, chunkSize)
	}

	// Se pintan en orden ascendente de id (RN-TERR-003). Como el primero en
	// pintar un tile es el de id menor, basta con no sobreescribir para que el
	// id menor gane los conflictos (RN-TERR-004).
	conflictos := make(map[[2]int64]*Overlap)
	for _, t := range s.ordered {
		id := uint16(t.ID)
		for y := t.MinY; y <= t.MaxY; y++ {
			fila := int(y) * int(width)
			for x := t.MinX; x <= t.MaxX; x++ {
				pos := fila + int(x)
				if previo := s.tiles[pos]; previo != 0 {
					clave := [2]int64{int64(previo), t.ID}
					if c, ok := conflictos[clave]; ok {
						c.Tiles++
					} else {
						conflictos[clave] = &Overlap{
							Kept: int64(previo), Discarded: t.ID, X: x, Y: y, Tiles: 1,
						}
					}
					continue
				}
				s.tiles[pos] = id
			}
		}
	}

	return s, ordenarSolapamientos(conflictos), nil
}

func validarRectangulo(t Territory, width, height int32) error {
	if t.ID < 1 || t.ID > MaxTerritories {
		return fmt.Errorf(
			"%w: id %d fuera de [1, %d]", ErrIDFueraDeRango, t.ID, MaxTerritories,
		)
	}
	if t.MinX > t.MaxX || t.MinY > t.MaxY {
		return fmt.Errorf(
			"%w: territorio %d con límites desordenados (%d,%d)-(%d,%d)",
			ErrRectanguloInvalido, t.ID, t.MinX, t.MinY, t.MaxX, t.MaxY,
		)
	}
	// La contención en el mundo NO puede ser un CHECK del esquema: las
	// dimensiones son configuración (EO_WORLD_WIDTH/HEIGHT) y la migración no
	// las conoce. Por eso se valida aquí (INV-TERR-001).
	if t.MinX < 0 || t.MinY < 0 || t.MaxX >= width || t.MaxY >= height {
		return fmt.Errorf(
			"%w: territorio %d (%d,%d)-(%d,%d) se sale del mundo %d × %d",
			ErrRectanguloInvalido, t.ID, t.MinX, t.MinY, t.MaxX, t.MaxY, width, height,
		)
	}
	return nil
}

// ordenarSolapamientos devuelve los conflictos en orden estable para que el log
// de dos arranques con los mismos datos sea idéntico.
func ordenarSolapamientos(m map[[2]int64]*Overlap) []Overlap {
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

// TerritoryAt resuelve el territorio que contiene el tile.
//
// Una consulta fuera del mundo devuelve "ninguno" y no entra en pánico
// (RN-TERR-005): las coordenadas llegan de mensajes del cliente y un índice
// fuera de rango sería una vía trivial de tirar el servidor.
func (s *Set) TerritoryAt(x, y int32) (Territory, bool) {
	if s == nil || x < 0 || y < 0 || x >= s.width || y >= s.height {
		return Territory{}, false
	}
	id := s.tiles[int(y)*int(s.width)+int(x)]
	if id == 0 {
		return Territory{}, false
	}
	t, ok := s.byID[int64(id)]
	return t, ok
}

// ByID devuelve un territorio por su identificador.
func (s *Set) ByID(id int64) (Territory, bool) {
	if s == nil {
		return Territory{}, false
	}
	t, ok := s.byID[id]
	return t, ok
}

// All devuelve los territorios en orden ascendente de id.
func (s *Set) All() []Territory {
	if s == nil {
		return nil
	}
	out := make([]Territory, len(s.ordered))
	copy(out, s.ordered)
	return out
}

// Len es el número de territorios del conjunto.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.ordered)
}

// FootprintOf devuelve los chunks que solapa el territorio, en orden estable.
func (s *Set) FootprintOf(id int64) []world.ChunkCoord {
	if s == nil {
		return nil
	}
	f, ok := s.footprints[id]
	if !ok {
		return nil
	}
	out := make([]world.ChunkCoord, len(f))
	copy(out, f)
	return out
}

// InChunks devuelve los territorios cuya huella intersecta alguno de los chunks
// dados, en orden ascendente de id.
//
// Es la consulta que decide qué territorios viajan en un world.snapshot y qué
// sesiones reciben un territory.update (RN-TERR-011 y RN-TERR-012).
func (s *Set) InChunks(chunks []world.ChunkCoord) []Territory {
	if s == nil || len(chunks) == 0 {
		return nil
	}
	quiere := make(map[world.ChunkCoord]struct{}, len(chunks))
	for _, c := range chunks {
		quiere[c] = struct{}{}
	}

	// Se recorre `ordered`, no el map de huellas: el resultado debe salir
	// siempre en el mismo orden.
	out := make([]Territory, 0, 4)
	for _, t := range s.ordered {
		for _, c := range s.footprints[t.ID] {
			if _, ok := quiere[c]; ok {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// ChunkFootprint calcula los chunks que solapa un territorio.
//
// La división es entera y NO un desplazamiento de bits: el tamaño de chunk es
// configuración (EO_CHUNK_SIZE, de 1 a 256) y no tiene por qué ser potencia de
// dos. Un `>> 5` funcionaría con el 32 por defecto y daría resultados
// silenciosamente incorrectos con cualquier otro valor.
func ChunkFootprint(t Territory, chunkSize int32) []world.ChunkCoord {
	if chunkSize <= 0 {
		return nil
	}
	minCX, maxCX := t.MinX/chunkSize, t.MaxX/chunkSize
	minCY, maxCY := t.MinY/chunkSize, t.MaxY/chunkSize

	out := make([]world.ChunkCoord, 0, int(maxCX-minCX+1)*int(maxCY-minCY+1))
	for cy := minCY; cy <= maxCY; cy++ {
		for cx := minCX; cx <= maxCX; cx++ {
			out = append(out, world.ChunkCoord{CX: cx, CY: cy})
		}
	}
	return out
}

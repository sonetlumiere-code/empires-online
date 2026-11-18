package protocol

import (
	"embed"
	"encoding/json"
	"fmt"
)

// SchemaFS embebe el JSON Schema generado desde packages/protocol (Zod).
//
// Estos archivos NO se editan a mano: los produce `pnpm run protocol:build`.
// Existen aquí porque go:embed no puede salir del módulo Go, y se versionan para
// que cualquier cambio de contrato aparezca en el diff de la PR.
//
//go:embed schema/v1/*.json
var SchemaFS embed.FS

// SchemaFile devuelve el contenido crudo de un artefacto de esquema.
func SchemaFile(name string) ([]byte, error) {
	data, err := SchemaFS.ReadFile("schema/v1/" + name)
	if err != nil {
		return nil, fmt.Errorf("esquema %q no embebido: %w", name, err)
	}
	return data, nil
}

// ExportedErrorCodes lee el catálogo de códigos de error exportado por el paquete
// TypeScript. Los contract tests lo comparan contra AllErrorCodes para que una
// divergencia rompa la CI en lugar de romper a los jugadores.
func ExportedErrorCodes() ([]string, error) {
	data, err := SchemaFile("error-codes.json")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Version int      `json:"version"`
		Codes   []string `json:"codes"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("error-codes.json malformado: %w", err)
	}
	if doc.Version != Version {
		return nil, fmt.Errorf("error-codes.json declara la versión %d y el servidor habla la %d", doc.Version, Version)
	}
	return doc.Codes, nil
}

// CivilizationInfo es una entrada del catálogo de presentación de las
// civilizaciones que exporta el paquete TypeScript (ADR-035 §7).
type CivilizationInfo struct {
	Code string `json:"code"`
	Name string `json:"name"`
	// Good es el bien que sólo elabora su taller (SPEC-CIVILIZATION-GOODS).
	Good string `json:"good"`
	// Unit es su unidad exclusiva (SPEC-CIVILIZATION-UNITS §4.1).
	Unit   string           `json:"unit"`
	Traits map[string]int32 `json:"traits"`
}

// ExportedCivilizations lee el catálogo de presentación de las civilizaciones.
// No es la fuente de los rasgos (lo es la tabla civilizations): un test de
// integración lo compara con ella para que el formulario de registro no mienta.
func ExportedCivilizations() ([]CivilizationInfo, error) {
	data, err := SchemaFile("civilizations.json")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Version       int                `json:"version"`
		Civilizations []CivilizationInfo `json:"civilizations"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("civilizations.json malformado: %w", err)
	}
	if doc.Version != Version {
		return nil, fmt.Errorf("civilizations.json declara la versión %d y el servidor habla la %d", doc.Version, Version)
	}
	return doc.Civilizations, nil
}

// TechnologyInfo es una entrada del catálogo de presentación de las
// tecnologías que exporta el paquete TypeScript (ADR-046 §1).
type TechnologyInfo struct {
	Code         string           `json:"code"`
	Name         string           `json:"name"`
	Era          string           `json:"era"`
	Civilization *string          `json:"civilization"`
	Costs        map[string]int64 `json:"costs"`
	ForeignGoods int64            `json:"foreignGoods"`
	OwnGood      int64            `json:"ownGood"`
	DurationMs   int64            `json:"durationMs"`
	Requires     []string         `json:"requires"`
	Effects      map[string]int32 `json:"effects"`
}

// ExportedTechnologies lee el catálogo de presentación de las tecnologías. No
// es la fuente (lo es la tabla technologies): un test de integración lo
// compara con ella para que el panel no mienta.
func ExportedTechnologies() ([]TechnologyInfo, error) {
	data, err := SchemaFile("technologies.json")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Version      int              `json:"version"`
		Technologies []TechnologyInfo `json:"technologies"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("technologies.json malformado: %w", err)
	}
	if doc.Version != Version {
		return nil, fmt.Errorf("technologies.json declara la versión %d y el servidor habla la %d", doc.Version, Version)
	}
	return doc.Technologies, nil
}

// UnitCost es una línea del coste de entrenamiento de una unidad.
type UnitCost struct {
	Resource string `json:"resource"`
	Amount   int64  `json:"amount"`
}

// UnitInfo es una entrada del catálogo de presentación de las unidades que
// exporta el paquete TypeScript (ADR-043 §7).
type UnitInfo struct {
	Code             string     `json:"code"`
	Name             string     `json:"name"`
	Attacks          bool       `json:"attacks"`
	Exclusive        bool       `json:"exclusive"`
	TrainCosts       []UnitCost `json:"trainCosts"`
	OwnGoodCost      int64      `json:"ownGoodCost"`
	TrainMs          int64      `json:"trainMs"`
	RequiresEra      *string    `json:"requiresEra"`
	MaxHP            int32      `json:"maxHp"`
	MsPerTile        int64      `json:"msPerTile"`
	AttackDamage     int32      `json:"attackDamage"`
	AttackIntervalMs int64      `json:"attackIntervalMs"`
	AttackRange      int32      `json:"attackRange"`
	LootCarry        int32      `json:"lootCarry"`
	PopulationCost   int32      `json:"populationCost"`
}

// ExportedUnits lee el catálogo de presentación de las unidades. No es la
// fuente de las estadísticas (lo es unit.catalog): un test lo compara con él
// para que el cliente no enseñe un perfil o un coste que no son.
func ExportedUnits() ([]UnitInfo, error) {
	data, err := SchemaFile("units.json")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Version int        `json:"version"`
		Units   []UnitInfo `json:"units"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("units.json malformado: %w", err)
	}
	if doc.Version != Version {
		return nil, fmt.Errorf("units.json declara la versión %d y el servidor habla la %d", doc.Version, Version)
	}
	return doc.Units, nil
}

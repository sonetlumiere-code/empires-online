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

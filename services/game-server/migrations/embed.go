// Package migrations embebe los archivos SQL de migración dentro del binario.
//
// Viven aquí, junto al SQL, para que exista UNA sola copia: duplicar los .sql en
// otra carpeta sólo para satisfacer a go:embed garantizaría que tarde o temprano
// las dos versiones divergieran.
//
// El artefacto desplegado es autosuficiente: no hay forma de desplegar el binario
// y olvidar copiar las migraciones que necesita.
package migrations

import "embed"

// FS contiene todos los archivos .sql de migración.
//
//go:embed *.sql
var FS embed.FS

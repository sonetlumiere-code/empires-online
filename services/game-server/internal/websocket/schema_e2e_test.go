package websocket_test

// Contrato servidor→cliente (M3, aceptación 10; EO-056): TODO mensaje que el
// servidor real emite en estos tests e2e se valida contra el JSON Schema que
// exporta @empires-online/protocol, el mismo que el cliente valida con Zod.
//
// La validación vive en conn.read, por donde pasa cada lectura del socket, así
// que ningún test puede saltársela. Y TestMain, cuando corre el paquete entero,
// exige además que cada tipo de mensaje del esquema se haya visto al menos una
// vez: un tipo que ningún test emite es un tipo cuyo contrato nadie comprueba.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

var (
	esquemaOnce sync.Once
	esquema     *jsonschema.Schema
	tipos       []string
	esquemaErr  error

	vistosMu sync.Mutex
	vistos   = map[string]int{}
)

func cargarEsquema() {
	raw, err := protocol.SchemaFile("server-message.schema.json")
	if err != nil {
		esquemaErr = err
		return
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		esquemaErr = err
		return
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	if err := c.AddResource("server-message.schema.json", doc); err != nil {
		esquemaErr = err
		return
	}
	esquema, esquemaErr = c.Compile("server-message.schema.json")
	if esquemaErr != nil {
		return
	}

	// Los tipos de mensaje del contrato: el `const` de `type` en cada rama.
	var s struct {
		Definitions struct {
			ServerMessage struct {
				AnyOf []struct {
					Properties struct {
						Type struct {
							Const string `json:"const"`
						} `json:"type"`
					} `json:"properties"`
				} `json:"anyOf"`
			} `json:"ServerMessage"`
		} `json:"definitions"`
	}
	if esquemaErr = json.Unmarshal(raw, &s); esquemaErr != nil {
		return
	}
	for _, rama := range s.Definitions.ServerMessage.AnyOf {
		if rama.Properties.Type.Const != "" {
			tipos = append(tipos, rama.Properties.Type.Const)
		}
	}
	if len(tipos) == 0 {
		esquemaErr = fmt.Errorf("el esquema no declara ningún tipo de mensaje")
	}
}

// validarMensajeDelServidor falla el test si el mensaje no cumple el contrato.
func validarMensajeDelServidor(t testing.TB, data []byte) {
	t.Helper()
	esquemaOnce.Do(cargarEsquema)
	if esquemaErr != nil {
		t.Fatalf("no se pudo cargar el esquema servidor→cliente: %v", esquemaErr)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("el servidor emitió JSON inválido: %v\n%s", err, data)
	}
	if err := esquema.Validate(v); err != nil {
		t.Fatalf("el servidor emitió un mensaje fuera del contrato:\n%s\n%v", data, err)
	}
	var cab struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(data, &cab)
	vistosMu.Lock()
	vistos[cab.Type]++
	vistosMu.Unlock()
}

// read lee el siguiente mensaje del servidor y lo valida contra el esquema.
func (c *conn) read() ([]byte, error) {
	c.t.Helper()
	_, data, err := c.c.ReadMessage()
	if err != nil {
		return nil, err
	}
	validarMensajeDelServidor(c.t, data)
	return data, nil
}

func TestMain(m *testing.M) {
	flag.Parse()
	code := m.Run()
	// Con -run se ejecuta un subconjunto: la cobertura de tipos sólo tiene
	// sentido sobre el paquete entero.
	if code == 0 && flag.Lookup("test.run").Value.String() == "" {
		esquemaOnce.Do(cargarEsquema)
		if esquemaErr != nil {
			fmt.Fprintf(os.Stderr, "esquema servidor→cliente: %v\n", esquemaErr)
			os.Exit(1)
		}
		var faltan []string
		for _, tipo := range tipos {
			if vistos[tipo] == 0 {
				faltan = append(faltan, tipo)
			}
		}
		sort.Strings(faltan)
		if len(faltan) > 0 {
			fmt.Fprintf(os.Stderr, "\nFAIL: ningún test e2e emite ni valida estos mensajes del contrato: %v\n", faltan)
			code = 1
		}
	}
	os.Exit(code)
}

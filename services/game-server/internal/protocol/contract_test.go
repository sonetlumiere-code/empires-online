package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/empires-online/empires-online/services/game-server/internal/protocol"
)

// Contract tests del lado servidor.
//
// La fuente de verdad del protocolo es packages/protocol (Zod). Aquí se comprueba
// que la implementación Go no ha derivado de ella. Si estos tests fallan, el
// contrato se rompió: hay que ejecutar `pnpm run protocol:build` y revisar el diff.
// Ver ../../../../docs/testing/contract-tests.md

// INV-SEC: el catálogo de códigos de error debe ser EXACTAMENTE el mismo en
// ambos lados. Un código que sólo existe en uno es un error que el otro no sabe
// interpretar.
func TestCatalogoDeErroresCoincideConElExportado(t *testing.T) {
	exported, err := protocol.ExportedErrorCodes()
	require.NoError(t, err, "¿ejecutaste `pnpm run protocol:build`?")

	require.ElementsMatch(t, exported, protocol.AllErrorCodes,
		"los códigos de error de Go y los de packages/protocol han divergido")
}

func TestEsquemasEmbebidosExistenYSonJSONValido(t *testing.T) {
	for _, name := range []string{"client-message.schema.json", "server-message.schema.json", "error-codes.json"} {
		t.Run(name, func(t *testing.T) {
			data, err := protocol.SchemaFile(name)
			require.NoError(t, err)
			require.NotEmpty(t, data)

			var doc any
			require.NoError(t, json.Unmarshal(data, &doc), "%s no es JSON válido", name)
		})
	}
}

// Los tipos de mensaje declarados en Go deben aparecer en el esquema exportado.
func TestTiposDeMensajePresentesEnElEsquema(t *testing.T) {
	clientSchema, err := protocol.SchemaFile("client-message.schema.json")
	require.NoError(t, err)
	serverSchema, err := protocol.SchemaFile("server-message.schema.json")
	require.NoError(t, err)

	client := string(clientSchema)
	server := string(serverSchema)

	for msgType := range protocol.ClientMessageTypes {
		require.Contains(t, client, `"`+msgType+`"`,
			"el tipo cliente→servidor %q no aparece en el esquema exportado", msgType)
	}

	serverTypes := []string{
		protocol.TypeSessionWelcome, protocol.TypeSessionPong, protocol.TypeSystemError,
		protocol.TypeWorldSnapshot, protocol.TypeEntitySpawn, protocol.TypeEntityUpdate,
		protocol.TypeEntityDespawn, protocol.TypeCityUpdate, protocol.TypeTerritoryUpdate,
		protocol.TypeUnitMoveAccepted, protocol.TypeUnitMoveRejected,
		protocol.TypeUnitMovementStarted, protocol.TypeUnitMovementCompleted,
		protocol.TypeUnitMovementCancelled,
	}
	for _, msgType := range serverTypes {
		require.Contains(t, server, `"`+msgType+`"`,
			"el tipo servidor→cliente %q no aparece en el esquema exportado", msgType)
	}
}

func TestEnvelopeSalienteSerializaConLosCamposDelContrato(t *testing.T) {
	out := protocol.NewOutbound(protocol.TypeUnitMoveAccepted, 7, 1_757_376_000_000,
		"3f2504e0-4f89-41d3-9a0c-0305e82c3301",
		protocol.UnitMoveAcceptedPayload{UnitID: 42, MovementID: 900})

	data, err := json.Marshal(out)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(data, &decoded))

	require.EqualValues(t, 1, decoded["v"])
	require.Equal(t, "unit.move.accepted", decoded["type"])
	require.EqualValues(t, 7, decoded["seq"])
	require.EqualValues(t, 1_757_376_000_000, decoded["ts"])
	require.Equal(t, "3f2504e0-4f89-41d3-9a0c-0305e82c3301", decoded["requestId"])
}

// Un mensaje que no responde a ningún comando no debe llevar requestId vacío:
// el campo se omite para no confundir al cliente.
func TestRequestIDSeOmiteCuandoNoAplica(t *testing.T) {
	out := protocol.NewOutbound(protocol.TypeEntityUpdate, 1, 1, "",
		protocol.EntityUpdatePayload{ID: 1})

	data, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(data), "requestId")
}

// EntityUpdate transporta SÓLO lo que cambió: los punteros nil se omiten.
func TestEntityUpdateEsUnDeltaDeVerdad(t *testing.T) {
	x, y := int32(10), int32(20)
	payload := protocol.EntityUpdatePayload{ID: 42, X: &x, Y: &y}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	s := string(data)
	require.Contains(t, s, `"x":10`)
	require.Contains(t, s, `"y":20`)
	require.NotContains(t, s, `"hp"`, "un campo sin cambios no debe viajar")
	require.NotContains(t, s, `"status"`)
	require.NotContains(t, s, `"movement"`)
}

// El payload de unit.movement.started debe llevar la polilínea COMPLETA: es lo
// que permite al cliente interpolar sin sondear al servidor.
func TestMovementStartedLlevaLaPolilineaCompleta(t *testing.T) {
	payload := protocol.UnitMovementStartedPayload{
		UnitID: 42,
		Movement: protocol.ActiveMovement{
			MovementID: 900,
			Path: []protocol.Waypoint{
				{X: 10, Y: 10, TMs: 0},
				{X: 11, Y: 10, TMs: 600},
				{X: 12, Y: 11, TMs: 1449},
			},
			StartTimeMs:   1_757_376_000_000,
			ArrivalTimeMs: 1_757_376_001_449,
			Target:        protocol.Tile{X: 12, Y: 11},
		},
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var decoded protocol.UnitMovementStartedPayload
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Len(t, decoded.Movement.Path, 3)
	require.EqualValues(t, 0, decoded.Movement.Path[0].TMs)
	require.EqualValues(t, 1449, decoded.Movement.Path[2].TMs)
	require.Equal(t, payload.Movement.ArrivalTimeMs, decoded.Movement.ArrivalTimeMs)
}

// El contrato de entrada NO tiene campo para la ruta: el cliente no puede
// aportarla ni por accidente ni a propósito.
func TestElComandoDeMovimientoNoAdmiteRuta(t *testing.T) {
	raw := []byte(`{"unitId":42,"target":{"x":1,"y":2},"path":[{"x":0,"y":0,"tMs":0}]}`)

	var payload protocol.UnitMovePayload
	require.NoError(t, json.Unmarshal(raw, &payload))

	require.EqualValues(t, 42, payload.UnitID)
	require.Equal(t, protocol.Tile{X: 1, Y: 2}, payload.Target)
	// La estructura simplemente no tiene dónde guardar una ruta del cliente.
	// La validación estricta contra el JSON Schema la rechaza en el boundary.
}

func TestPreAuthSoloAdmiteElHandshake(t *testing.T) {
	require.True(t, protocol.IsPreAuth(protocol.TypeSessionHello))
	for _, msgType := range []string{
		protocol.TypeUnitMove, protocol.TypeUnitCancelMove,
		protocol.TypeSessionPing, protocol.TypeSessionView,
	} {
		require.False(t, protocol.IsPreAuth(msgType),
			"%s no puede aceptarse antes de autenticar", msgType)
	}
}

func TestCatalogoDeComandosEsCerrado(t *testing.T) {
	require.Len(t, protocol.ClientMessageTypes, 5)
	for _, unknown := range []string{"unit.teleport", "admin.grant", "", "world.snapshot"} {
		require.False(t, protocol.ClientMessageTypes[unknown],
			"%q no debe aceptarse como comando del cliente", unknown)
	}
}

func TestCodigosDeCierreSonDelRangoPrivado(t *testing.T) {
	codes := []int{
		protocol.CloseInvalidMessage, protocol.CloseUnauthenticated, protocol.CloseForbidden,
		protocol.CloseHandshakeTimeout, protocol.CloseRateLimited, protocol.CloseInternalError,
	}
	for _, c := range codes {
		require.GreaterOrEqual(t, c, 4000, "los códigos de aplicación viven en 4000-4999")
		require.LessOrEqual(t, c, 4999)
	}
}

func TestErrorDeProtocoloLlevaCodigoEstable(t *testing.T) {
	err := protocol.NewError(protocol.CodeUnitNotOwned, "la unidad no te pertenece").
		WithDetails(map[string]any{"unitId": 42})

	require.Equal(t, protocol.CodeUnitNotOwned, err.Code)
	require.Contains(t, err.Error(), "UNIT_NOT_OWNED")
	require.EqualValues(t, 42, err.Details["unitId"])
}

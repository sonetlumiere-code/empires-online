// Package auth verifica la identidad de quien abre una conexión de juego.
//
// El Game Server NO gestiona contraseñas ni sesiones de navegador: eso vive en el
// frontend. El frontend autentica al usuario y emite un GAME TICKET de vida muy
// corta; aquí sólo se comprueba que ese ticket es auténtico, no ha caducado y no
// se ha usado antes. Ver ../../../../docs/decisions/ADR-010-authentication-game-ticket.md
package auth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/empires-online/empires-online/services/game-server/internal/clock"
)

// Audience identifica al destinatario legítimo del ticket. Un token emitido para
// otro servicio no sirve aquí.
const Audience = "game-server"

// Errores de autenticación. Todos se traducen al MISMO código UNAUTHORIZED de
// cara al cliente: distinguirlos ayudaría a un atacante a afinar sus intentos.
var (
	ErrInvalidTicket  = errors.New("ticket inválido")
	ErrExpiredTicket  = errors.New("ticket caducado")
	ErrReplayedTicket = errors.New("ticket ya utilizado")
)

// Claims son los datos que el servidor extrae de un ticket verificado.
type Claims struct {
	PlayerID uuid.UUID
	JTI      string
	IssuedAt time.Time
	Expires  time.Time
}

// Verifier valida tickets.
type Verifier struct {
	secret []byte
	clock  clock.Clock
}

// NewVerifier crea el verificador.
func NewVerifier(secret string, clk clock.Clock) *Verifier {
	return &Verifier{secret: []byte(secret), clock: clk}
}

// Verify comprueba firma, caducidad y audiencia, y devuelve los claims.
//
// NO comprueba la reutilización: eso exige estado compartido y lo hace
// Consumer.Consume con Redis. Separarlos permite testear la criptografía sin
// infraestructura.
func (v *Verifier) Verify(tokenString string) (Claims, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithAudience(Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		// El reloj inyectado también manda aquí: así los tests de caducidad no
		// dependen del reloj de la máquina.
		jwt.WithTimeFunc(v.clock.Now),
	)

	token, err := parser.Parse(tokenString, func(t *jwt.Token) (any, error) {
		return v.secret, nil
	})
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return Claims{}, fmt.Errorf("%w: %v", ErrExpiredTicket, err)
		}
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidTicket, err)
	}
	if !token.Valid {
		return Claims{}, ErrInvalidTicket
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return Claims{}, ErrInvalidTicket
	}

	sub, err := claims.GetSubject()
	if err != nil || sub == "" {
		return Claims{}, fmt.Errorf("%w: falta el subject", ErrInvalidTicket)
	}
	playerID, err := uuid.Parse(sub)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: subject %q no es un uuid", ErrInvalidTicket, sub)
	}

	jti, _ := claims["jti"].(string)
	if jti == "" {
		// Sin jti no se puede impedir el replay, así que el ticket no sirve.
		return Claims{}, fmt.Errorf("%w: falta el jti", ErrInvalidTicket)
	}

	out := Claims{PlayerID: playerID, JTI: jti}
	if exp, err := claims.GetExpirationTime(); err == nil && exp != nil {
		out.Expires = exp.Time
	}
	if iat, err := claims.GetIssuedAt(); err == nil && iat != nil {
		out.IssuedAt = iat.Time
	}
	return out, nil
}

// TicketConsumer registra los tickets ya canjeados.
type TicketConsumer interface {
	// Consume devuelve false si el jti ya se había usado.
	Consume(ctx context.Context, jti string) (bool, error)
}

// Authenticator combina verificación criptográfica y protección contra replay.
type Authenticator struct {
	verifier *Verifier
	consumer TicketConsumer
}

// NewAuthenticator crea el autenticador completo.
func NewAuthenticator(v *Verifier, c TicketConsumer) *Authenticator {
	return &Authenticator{verifier: v, consumer: c}
}

// Authenticate valida un ticket y lo marca como consumido de forma atómica.
func (a *Authenticator) Authenticate(ctx context.Context, ticket string) (Claims, error) {
	claims, err := a.verifier.Verify(ticket)
	if err != nil {
		return Claims{}, err
	}
	fresh, err := a.consumer.Consume(ctx, claims.JTI)
	if err != nil {
		return Claims{}, fmt.Errorf("no se pudo verificar la unicidad del ticket: %w", err)
	}
	if !fresh {
		return Claims{}, ErrReplayedTicket
	}
	return claims, nil
}

// Issuer emite tickets.
//
// En producción el emisor es el frontend Next.js. Esta implementación existe para
// los tests, las herramientas de desarrollo y para documentar exactamente qué
// forma debe tener un ticket válido.
type Issuer struct {
	secret []byte
	ttl    time.Duration
	clock  clock.Clock
}

// NewIssuer crea el emisor.
func NewIssuer(secret string, ttl time.Duration, clk clock.Clock) *Issuer {
	return &Issuer{secret: []byte(secret), ttl: ttl, clock: clk}
}

// Issue emite un ticket para un jugador.
func (i *Issuer) Issue(playerID uuid.UUID) (string, error) {
	now := i.clock.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": playerID.String(),
		"jti": uuid.NewString(),
		"aud": Audience,
		"iat": now.Unix(),
		"exp": now.Add(i.ttl).Unix(),
	})
	signed, err := token.SignedString(i.secret)
	if err != nil {
		return "", fmt.Errorf("firmar el ticket: %w", err)
	}
	return signed, nil
}

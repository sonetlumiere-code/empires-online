package message

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

// Limiter cuenta, por jugador y sumando todas sus sesiones, los envíos del
// último minuto y las conversaciones nuevas de la última hora (RN-MSG-005,
// ADR-038 §5). Vive en la RAM del proceso: tras un reinicio empieza de cero.
//
// Reservar devuelve una función que deshace la reserva: un envío rechazado o
// fallido no gasta nada. El instante lo pone quien llama, así que el paquete no
// lee el reloj.
type Limiter struct {
	perMinute  int
	newPerHour int

	mu       sync.Mutex
	byPlayer map[uuid.UUID]*windows
}

type windows struct {
	sends []time.Time
	newer []time.Time
}

// NewLimiter crea el contador con los dos límites. Un límite de 0 no limita.
func NewLimiter(perMinute, newPerHour int) *Limiter {
	return &Limiter{perMinute: perMinute, newPerHour: newPerHour, byPlayer: map[uuid.UUID]*windows{}}
}

// ReserveSend reserva un envío del jugador en el minuto que acaba en now. Si no
// queda, ok es falso y no se reserva nada.
func (l *Limiter) ReserveSend(player uuid.UUID, now time.Time) (release func(), ok bool) {
	return l.reserve(player, now, l.perMinute, time.Minute, func(w *windows) *[]time.Time { return &w.sends })
}

// ReserveNewConversation reserva una conversación nueva del jugador en la hora
// que acaba en now.
func (l *Limiter) ReserveNewConversation(player uuid.UUID, now time.Time) (release func(), ok bool) {
	return l.reserve(player, now, l.newPerHour, time.Hour, func(w *windows) *[]time.Time { return &w.newer })
}

func (l *Limiter) reserve(player uuid.UUID, now time.Time, limit int, span time.Duration, pick func(*windows) *[]time.Time) (func(), bool) {
	if limit <= 0 {
		return func() {}, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.byPlayer[player]
	if w == nil {
		w = &windows{}
		l.byPlayer[player] = w
	}
	l.prune(w, now)
	list := pick(w)
	if len(*list) >= limit {
		l.forgetIfEmpty(player, w)
		return nil, false
	}
	*list = append(*list, now)
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			w := l.byPlayer[player]
			if w == nil {
				return
			}
			list := pick(w)
			for i := len(*list) - 1; i >= 0; i-- {
				if (*list)[i].Equal(now) {
					*list = append((*list)[:i], (*list)[i+1:]...)
					break
				}
			}
			l.forgetIfEmpty(player, w)
		})
	}, true
}

// prune quita lo que ya salió de cada ventana. Las listas están en orden de
// llegada, que es el del reloj salvo saltos hacia atrás, y un salto hacia atrás
// sólo hace la ventana más estricta un rato.
func (l *Limiter) prune(w *windows, now time.Time) {
	w.sends = since(w.sends, now.Add(-time.Minute))
	w.newer = since(w.newer, now.Add(-time.Hour))
}

func since(list []time.Time, from time.Time) []time.Time {
	i := 0
	for i < len(list) && !list[i].After(from) {
		i++
	}
	return list[i:]
}

// forgetIfEmpty borra al jugador sin nada contado: el mapa no crece con cada
// jugador que escribió alguna vez.
func (l *Limiter) forgetIfEmpty(player uuid.UUID, w *windows) {
	if len(w.sends) == 0 && len(w.newer) == 0 {
		delete(l.byPlayer, player)
	}
}

// players devuelve cuántos jugadores tienen algo contado. Sólo para los tests.
func (l *Limiter) players() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.byPlayer)
}

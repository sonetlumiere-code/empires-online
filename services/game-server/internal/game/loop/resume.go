package loop

import "time"

// ResumeTick decide desde qué tick arranca el loop (ADR-015).
//
// El contador avanza en uno por tick ejecutado y el loop nunca ejecuta más de
// un tick por período, así que en todo momento tick <= (ahora − epoch) /
// período. El valor derivado del reloj es por tanto una cota superior de todo
// tick ya emitido, se apagara el proceso como se apagara, y arrancar desde él
// garantiza que ningún número se repite tras una caída. `saved` —el tick que
// guardó el último apagado ordenado— es sólo un suelo frente a un reloj que va
// hacia atrás; `clockBehind` lo señala (INV-WORLD-004).
func ResumeTick(saved uint64, epochMs, nowMs int64, period time.Duration) (tick uint64, clockBehind bool) {
	var derived uint64
	if p := period.Milliseconds(); p > 0 && nowMs > epochMs {
		derived = uint64((nowMs - epochMs) / p)
	}
	if derived < saved {
		return saved, true
	}
	return derived, false
}

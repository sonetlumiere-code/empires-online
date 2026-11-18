package simulation

// Ganchos para los tests de package simulation_test: sólo se compilan con los
// tests.

// KillByCastle aplica la muerte de la unidad victimID firmada por el castillo
// castleID, como lo hace applyCastleHits con el golpe mortal de un castillo. Hoy
// un castillo sólo dispara a quien ataca a su dueño y una caravana nunca ataca,
// así que la rama de kill para una caravana muerta por un castillo
// (RN-CLANWAR-007: no puntúa) sólo se alcanza por aquí. Devuelve false si la
// unidad, el castillo o su ciudad no existen.
func (s *Simulation) KillByCastle(victimID, castleID int64) bool {
	v, ok := s.state.Unit(victimID)
	if !ok {
		return false
	}
	b, ok := s.state.buildings[castleID]
	if !ok {
		return false
	}
	c, ok := s.state.City(b.CityID)
	if !ok {
		return false
	}
	s.kill(v, killer{castle: b, owner: c.OwnerPlayerID}, s.deps.Clock.NowMs())
	return true
}

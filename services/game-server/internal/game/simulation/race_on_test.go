//go:build race

package simulation_test

// conDetectorDeCarreras indica que la suite corre con -race. El detector
// multiplica el coste de CPU, así que un presupuesto de tiempo de pared no se
// puede medir con él.
const conDetectorDeCarreras = true

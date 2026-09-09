package persistence

import "github.com/jackc/pgx/v5"

// pgxTx existe sólo para no repetir el tipo completo de pgx en las firmas de este
// paquete. No añade abstracción: es un alias.
type pgxTx = pgx.Tx

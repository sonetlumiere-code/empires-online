-- SPEC-EVENT-LOG (docs/specs/event-log.md), ADR-028 §7: el registro de lo que
-- le pasó a cada jugador, leído de world_events.
--
-- counterpart_player_id: el otro jugador implicado en un suceso (el autor de una
-- muerte o de un saqueo, el otro firmante de un tratado). player_id sigue
-- significando lo mismo que antes. Su índice es el par del que ya existe por
-- player_id: un registro es player_id = P OR counterpart_player_id = P.
--
-- event_log_seen_id: el último evento que el jugador dio por visto. Sólo avanza,
-- y nunca más allá del tope marcable de la página que lo permitió (INV-PERSIST-006).

ALTER TABLE world_events
    ADD COLUMN counterpart_player_id uuid REFERENCES players (id) ON DELETE SET NULL;

CREATE INDEX world_events_counterpart_idx ON world_events (counterpart_player_id, occurred_at DESC)
    WHERE counterpart_player_id IS NOT NULL;

ALTER TABLE players
    ADD COLUMN event_log_seen_id bigint NOT NULL DEFAULT 0 CHECK (event_log_seen_id >= 0);

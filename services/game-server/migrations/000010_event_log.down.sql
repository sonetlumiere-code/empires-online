-- Retira el registro y deja el esquema de 000009.
ALTER TABLE players DROP COLUMN event_log_seen_id;

DROP INDEX world_events_counterpart_idx;

ALTER TABLE world_events DROP COLUMN counterpart_player_id;

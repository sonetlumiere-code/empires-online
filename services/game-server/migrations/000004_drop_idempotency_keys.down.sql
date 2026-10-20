-- Recrea idempotency_keys exactamente como la definía 000001_initial_schema.

CREATE TABLE idempotency_keys (
    player_id   uuid        NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    request_id  uuid        NOT NULL,
    message_type text       NOT NULL,
    response    jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    PRIMARY KEY (player_id, request_id)
);

CREATE INDEX idempotency_keys_expiry_idx ON idempotency_keys (expires_at);

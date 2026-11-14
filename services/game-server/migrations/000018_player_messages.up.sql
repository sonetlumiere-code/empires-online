-- SPEC-MESSAGES (docs/specs/messages.md), ADR-038: mensajes de texto entre dos
-- jugadores, lo leído de cada conversación y los bloqueos. No son estado del
-- mundo: los escribe y los lee la conexión, nunca la simulación.
--
-- La conversación no es una fila: es la pareja (player_low, player_high), en el
-- orden de uuid de PostgreSQL, y la de A con B es la de B con A. El envío y el
-- bloqueo de una pareja toman un candado de transacción por ella, así que los id
-- de una conversación se confirman en orden (INV-PERSIST-007) y ningún mensaje
-- cruza un bloqueo (INV-SEC-011).

CREATE TABLE player_messages (
    id                  bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sender_player_id    uuid        NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    recipient_player_id uuid        NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    player_low          uuid        NOT NULL,
    player_high         uuid        NOT NULL,
    body                text        NOT NULL,
    request_id          uuid        NOT NULL,
    sent_at             timestamptz NOT NULL DEFAULT now(),

    -- La pareja ordenada es la del remitente y el destinatario, que son
    -- distintos (el orden estricto lo impide).
    CONSTRAINT player_messages_pair_canonical CHECK (player_low < player_high),
    CONSTRAINT player_messages_pair_matches CHECK (
        (player_low = sender_player_id AND player_high = recipient_player_id)
        OR (player_low = recipient_player_id AND player_high = sender_player_id)),
    -- RN-MSG-003: de 1 a 500 caracteres, ya recortado.
    CONSTRAINT player_messages_body_length CHECK (char_length(body) BETWEEN 1 AND 500),
    -- INV-SEC-007: un requestId repetido no guarda un segundo mensaje.
    CONSTRAINT player_messages_request_once UNIQUE (sender_player_id, request_id)
);

-- Una conversación por su pareja, de lo más reciente a lo más antiguo; la
-- bandeja de un jugador entra por player_low o por player_high.
CREATE INDEX player_messages_pair_idx ON player_messages (player_low, player_high, id DESC);
CREATE INDEX player_messages_high_idx ON player_messages (player_high, player_low, id DESC);

-- Lo leído: hasta qué mensaje leyó player_id su conversación con
-- other_player_id. Sólo avanza (GREATEST).
CREATE TABLE message_reads (
    player_id       uuid   NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    other_player_id uuid   NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    read_up_to_id   bigint NOT NULL DEFAULT 0,

    PRIMARY KEY (player_id, other_player_id),
    CONSTRAINT message_reads_distinct CHECK (player_id <> other_player_id),
    CONSTRAINT message_reads_non_negative CHECK (read_up_to_id >= 0)
);

-- player_id tiene bloqueado a blocked_player_id: no hay mensajes entre ellos en
-- ningún sentido (RN-MSG-004).
CREATE TABLE player_blocks (
    player_id         uuid        NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    blocked_player_id uuid        NOT NULL REFERENCES players (id) ON DELETE CASCADE,
    created_at        timestamptz NOT NULL DEFAULT now(),

    PRIMARY KEY (player_id, blocked_player_id),
    CONSTRAINT player_blocks_distinct CHECK (player_id <> blocked_player_id)
);

-- Quién tiene bloqueado a un jugador, para el envío.
CREATE INDEX player_blocks_blocked_idx ON player_blocks (blocked_player_id, player_id);

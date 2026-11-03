-- Retira los tratados en caliente y deja el esquema de 000008. Las filas en los
-- estados nuevos se borran: el CHECK de 000001 no los admite.
DROP INDEX treaties_unique_request;
DROP INDEX treaties_one_offer_per_pair;
DROP INDEX treaties_one_active_per_pair;

DELETE FROM treaties WHERE status IN ('REJECTED', 'WITHDRAWN');

ALTER TABLE treaties DROP CONSTRAINT treaties_proposer_in_pair;
ALTER TABLE treaties DROP CONSTRAINT treaties_status_valid;
ALTER TABLE treaties ADD CONSTRAINT treaties_status_valid
    CHECK (status IN ('PROPOSED', 'ACTIVE', 'EXPIRED', 'BROKEN'));

CREATE UNIQUE INDEX treaties_one_active_per_pair_and_type
    ON treaties (player_a_id, player_b_id, treaty_type)
    WHERE status = 'ACTIVE';

ALTER TABLE treaties
    DROP COLUMN closed_at,
    DROP COLUMN break_effective_at,
    DROP COLUMN request_id,
    DROP COLUMN proposed_by;

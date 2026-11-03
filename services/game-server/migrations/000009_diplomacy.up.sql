-- SPEC-DIPLOMACY (docs/specs/diplomacy.md), ADR-027 §4: tratados que proponen,
-- aceptan, rechazan, retiran y rompen los jugadores.
--
-- proposed_by: quién propuso (el par va en orden canónico y no lo dice). NULL en
-- los tratados administrativos. request_id: idempotencia de treaty.propose.
-- break_effective_at: con valor, el tratado está en preaviso y deja de valer en
-- ese instante. closed_at: cuándo se rechazó, retiró o caducó una oferta.

ALTER TABLE treaties
    ADD COLUMN proposed_by        uuid REFERENCES players (id) ON DELETE CASCADE,
    ADD COLUMN request_id         text,
    ADD COLUMN break_effective_at timestamptz,
    ADD COLUMN closed_at          timestamptz;

-- Los dos finales nuevos de una oferta.
ALTER TABLE treaties DROP CONSTRAINT treaties_status_valid;
ALTER TABLE treaties ADD CONSTRAINT treaties_status_valid
    CHECK (status IN ('PROPOSED', 'ACTIVE', 'EXPIRED', 'BROKEN', 'REJECTED', 'WITHDRAWN'));

-- Quien propone es uno de los dos firmantes.
ALTER TABLE treaties ADD CONSTRAINT treaties_proposer_in_pair
    CHECK (proposed_by IS NULL OR proposed_by IN (player_a_id, player_b_id));

-- INV-DIPLO-002: como mucho un tratado vigente y una oferta pendiente por par.
-- Sustituye al único por par y tipo, que permitía un pacto y una alianza a la vez.
DROP INDEX treaties_one_active_per_pair_and_type;
CREATE UNIQUE INDEX treaties_one_active_per_pair
    ON treaties (player_a_id, player_b_id) WHERE status = 'ACTIVE';
CREATE UNIQUE INDEX treaties_one_offer_per_pair
    ON treaties (player_a_id, player_b_id) WHERE status = 'PROPOSED';

CREATE UNIQUE INDEX treaties_unique_request
    ON treaties (proposed_by, request_id) WHERE request_id IS NOT NULL;

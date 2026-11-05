-- SPEC-CIVILIZATION (docs/specs/civilizations.md), ADR-035: los rasgos de
-- civilización se aplican en el juego.
--
-- civilizations.traits pasa de multiplicadores en coma flotante a enteros por
-- mil (1000 = sin efecto), con las claves de los sistemas que modifican
-- (INV-PLAYER-012). Los valores son los de 000002 por mil, salvo el persa:
-- economy.trade_yield no se aplica, porque multiplicar lo que se recibe de otro
-- jugador crearía recurso de la nada, y lo sustituye economy.gather_speed
-- (SPEC-CIVILIZATION Q5). El economy.gather_rate 1.00 romano desaparece: 1000 es
-- lo mismo que no declararlo.
--
-- La civilización de un jugador no cambia después del alta (INV-PLAYER-011):
-- un trigger rechaza cualquier UPDATE que la cambie.

UPDATE civilizations SET traits = '{"unit.move_speed": 1000, "production.speed": 1100}'::jsonb
 WHERE code = 'ROMAN';
UPDATE civilizations SET traits = '{"unit.move_speed": 950, "production.speed": 1000, "building.house_hp": 1200}'::jsonb
 WHERE code = 'BYZANTINE';
UPDATE civilizations SET traits = '{"unit.move_speed": 1050, "production.speed": 950, "economy.gather_speed": 1100}'::jsonb
 WHERE code = 'PERSIAN';
UPDATE civilizations SET traits = '{"unit.move_speed": 1100, "production.speed": 900, "combat.loot_carry": 1150}'::jsonb
 WHERE code = 'NORSE';

CREATE FUNCTION players_civilization_immutable() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.civilization_id IS DISTINCT FROM OLD.civilization_id THEN
        RAISE EXCEPTION 'la civilización de un jugador no cambia después del alta (INV-PLAYER-011)'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER players_civilization_immutable
    BEFORE UPDATE OF civilization_id ON players
    FOR EACH ROW EXECUTE FUNCTION players_civilization_immutable();

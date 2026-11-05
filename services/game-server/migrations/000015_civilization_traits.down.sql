-- Retira los rasgos de E13 y deja el esquema de 000014.
--
-- civilizations.traits vuelve a los multiplicadores de 000002, que ningún
-- sistema anterior a E13 lee, y la civilización de un jugador vuelve a poder
-- cambiarse. Los jugadores conservan la civilización que eligieron.

DROP TRIGGER players_civilization_immutable ON players;
DROP FUNCTION players_civilization_immutable();

UPDATE civilizations SET traits = '{"unit.move_speed": 1.00, "build.speed": 1.10, "economy.gather_rate": 1.00}'::jsonb
 WHERE code = 'ROMAN';
UPDATE civilizations SET traits = '{"unit.move_speed": 0.95, "build.speed": 1.00, "defense.wall_hp": 1.20}'::jsonb
 WHERE code = 'BYZANTINE';
UPDATE civilizations SET traits = '{"unit.move_speed": 1.05, "build.speed": 0.95, "economy.trade_yield": 1.15}'::jsonb
 WHERE code = 'PERSIAN';
UPDATE civilizations SET traits = '{"unit.move_speed": 1.10, "build.speed": 0.90, "combat.raid_bonus": 1.15}'::jsonb
 WHERE code = 'NORSE';

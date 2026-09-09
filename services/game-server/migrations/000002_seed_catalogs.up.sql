-- Datos semilla de los catálogos.
--
-- Son datos de juego, no datos de prueba: el servidor no puede arrancar sin ellos.
-- Van en una migración para que cualquier entorno (local, CI, producción) tenga
-- exactamente el mismo catálogo.

BEGIN;

-- ─────────────────────────────────────────────────────────────
-- Civilizaciones
--
-- `traits` es data-driven a propósito. Los sistemas consultan la clave que les
-- concierne y aplican el modificador; nunca comparan el código de la civilización.
-- Las claves son multiplicadores (1.0 = sin efecto).
-- ─────────────────────────────────────────────────────────────
INSERT INTO civilizations (id, code, name, traits) VALUES
    (1, 'ROMAN',     'Romanos',   '{"unit.move_speed": 1.00, "build.speed": 1.10, "economy.gather_rate": 1.00}'),
    (2, 'BYZANTINE', 'Bizantinos','{"unit.move_speed": 0.95, "build.speed": 1.00, "defense.wall_hp": 1.20}'),
    (3, 'PERSIAN',   'Persas',    '{"unit.move_speed": 1.05, "build.speed": 0.95, "economy.trade_yield": 1.15}'),
    (4, 'NORSE',     'Nórdicos',  '{"unit.move_speed": 1.10, "build.speed": 0.90, "combat.raid_bonus": 1.15}')
ON CONFLICT (id) DO NOTHING;

-- ─────────────────────────────────────────────────────────────
-- Facciones globales (ortogonales a la civilización)
-- ─────────────────────────────────────────────────────────────
INSERT INTO factions (id, code, name) VALUES
    (1, 'ORDER',   'Orden'),
    (2, 'CHAOS',   'Caos'),
    (3, 'NEUTRAL', 'Neutral')
ON CONFLICT (id) DO NOTHING;

-- ─────────────────────────────────────────────────────────────
-- Eras. El límite de población sale de aquí, no del código.
-- ─────────────────────────────────────────────────────────────
INSERT INTO eras (id, code, name, ordinal, population_cap) VALUES
    (1, 'STONE_AGE',  'Edad de Piedra',  1, 20),
    (2, 'BRONZE_AGE', 'Edad de Bronce',  2, 50),
    (3, 'IRON_AGE',   'Edad de Hierro',  3, 100),
    (4, 'CASTLE_AGE', 'Edad de Castillos', 4, 150)
ON CONFLICT (id) DO NOTHING;

COMMIT;

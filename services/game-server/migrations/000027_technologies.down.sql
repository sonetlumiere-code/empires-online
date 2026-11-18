-- Retira el árbol tecnológico y deja el esquema de 000026.
--
-- Se pierden las tecnologías de cada jugador y las investigaciones en curso, con
-- lo que pagaron: un servidor anterior no las conoce.

DROP TRIGGER player_technologies_civ_guard ON player_technologies;
DROP FUNCTION player_technologies_civ_guard();
DROP TRIGGER city_era_advances_research_guard ON city_era_advances;
DROP FUNCTION city_era_advances_research_guard();
DROP TRIGGER city_research_guard ON city_research;
DROP FUNCTION city_research_guard();

DROP TABLE player_technologies;
DROP TABLE city_research;
DROP TABLE technologies;

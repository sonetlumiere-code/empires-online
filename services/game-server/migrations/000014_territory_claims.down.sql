-- Retira la conquista y deja el esquema de 000013.
--
-- Se pierden las reclamaciones, también las activas, y su historia. Los
-- territorios ya conquistados conservan su dueño en territory_control y sus
-- eventos en world_events; los límites de población que subieron se quedan
-- como están, y la validación de carga de un servidor anterior los recontaría
-- sin el término de los territorios. contested vuelve a no tener productor: se
-- deja a false para que INV-TERR-006 vuelva a valer tal como era.

DROP TABLE territory_claims;
UPDATE territory_control SET contested = false WHERE contested;

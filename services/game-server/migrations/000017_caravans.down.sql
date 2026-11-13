-- Retira las caravanas y deja el esquema de 000016.
--
-- Nada se pierde al bajar: la carga de cada caravana en camino entra en el
-- almacén de su destino de ahora, como si hubiera llegado, y su unidad se
-- borra. La versión del stock sube para que ninguna escritura vieja la pise.

UPDATE city_stocks s
   SET amount = s.amount + c.amount, version = s.version + 1
  FROM (
      SELECT k.to_city_id AS city_id, g.resource, sum(g.amount) AS amount
        FROM caravans k
        JOIN caravan_cargo g ON g.caravan_id = k.id
       WHERE k.status = 'IN_TRANSIT'
       GROUP BY k.to_city_id, g.resource
  ) c
 WHERE s.city_id = c.city_id AND s.resource = c.resource;

DROP TABLE caravan_cargo;
DROP TABLE caravans;

-- Las unidades de las que seguían en camino (las entregadas ya no tienen fila).
-- Después de borrar caravans: su FK pondría unit_id a NULL en una caravana
-- IN_TRANSIT, que su CHECK no admite.
DELETE FROM units WHERE unit_type = 'CARAVAN';

-- Retira el comercio y deja el esquema de 000012.
--
-- Se pierden las ofertas, también las pendientes, y su historia. Lo que ya
-- movieron los tratos aceptados se queda en city_stocks, y sus eventos en
-- world_events. Los tratados TRADE que se firmaron siguen siendo filas válidas
-- de treaties: el tipo existe desde 000001.

DROP TABLE trade_offer_lines;
DROP TABLE trade_offers;

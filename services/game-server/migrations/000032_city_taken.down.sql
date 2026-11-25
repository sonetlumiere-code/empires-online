DROP INDEX IF EXISTS cities_taken_once;
ALTER TABLE cities DROP CONSTRAINT IF EXISTS cities_taken_from_other;
ALTER TABLE cities DROP COLUMN IF EXISTS taken_from_city_id;

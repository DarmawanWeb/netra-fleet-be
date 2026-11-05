-- Map modes (fixed by the license): robot maps (map.yaml + map.pgm) as floor plans of kind pgm,
-- and the georeference of a gps deployment.

-- floorplan: an uploaded layout calibrated by hand; pgm: a robot map placed by its map.yaml.
ALTER TABLE floor_plans ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'floorplan'
    CHECK (kind IN ('floorplan', 'pgm'));
-- pgm: metres per pixel and the map frame pose of the lower-left pixel, as in map.yaml.
ALTER TABLE floor_plans ADD COLUMN IF NOT EXISTS resolution_m DOUBLE PRECISION;
ALTER TABLE floor_plans ADD COLUMN IF NOT EXISTS map_origin_x DOUBLE PRECISION;
ALTER TABLE floor_plans ADD COLUMN IF NOT EXISTS map_origin_y DOUBLE PRECISION;
ALTER TABLE floor_plans ADD COLUMN IF NOT EXISTS map_origin_yaw DOUBLE PRECISION;

-- One row: how the robot map frame lies on the earth, and what to draw under it.
CREATE TABLE IF NOT EXISTS geo_site (
    id                  SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    -- [{label, x, y, lat, lon}]: the same places in map metres and WGS84 degrees
    control_points      JSONB NOT NULL DEFAULT '[]',
    -- compass heading of the map x axis, degrees from north; used with a single control point
    heading_deg         DOUBLE PRECISION,
    -- fitted, NULL until there is a fit: east = scale·(cos θ·x − sin θ·y) + east_m,
    -- north = scale·(sin θ·x + cos θ·y) + north_m, in the tangent plane at (ref_lat, ref_lon)
    fit_method          TEXT CHECK (fit_method IN ('points', 'heading')),
    ref_lat             DOUBLE PRECISION,
    ref_lon             DOUBLE PRECISION,
    scale               DOUBLE PRECISION,
    rotation_rad        DOUBLE PRECISION,
    east_m              DOUBLE PRECISION,
    north_m             DOUBLE PRECISION,
    rms_m               DOUBLE PRECISION,
    -- tiles under the map: osm, satellite (Esri World Imagery), a custom URL template, or none
    basemap             TEXT NOT NULL DEFAULT 'osm' CHECK (basemap IN ('osm', 'satellite', 'custom', 'none')),
    basemap_url         TEXT NOT NULL DEFAULT '',
    basemap_attribution TEXT NOT NULL DEFAULT '',
    basemap_max_zoom    INT NOT NULL DEFAULT 19,
    -- an image of the site (drone or aerial, north up) over its WGS84 bounds; works offline
    overlay_path        TEXT,
    overlay_type        TEXT,
    overlay_north       DOUBLE PRECISION,
    overlay_south       DOUBLE PRECISION,
    overlay_east        DOUBLE PRECISION,
    overlay_west        DOUBLE PRECISION,
    overlay_opacity     DOUBLE PRECISION NOT NULL DEFAULT 1,
    overlay_updated_at  TIMESTAMPTZ,
    updated_by          TEXT NOT NULL DEFAULT '',
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Robots that report GPS: the position of each history sample, for trails and track replay.
ALTER TABLE telemetry ADD COLUMN IF NOT EXISTS lat DOUBLE PRECISION;
ALTER TABLE telemetry ADD COLUMN IF NOT EXISTS lon DOUBLE PRECISION;

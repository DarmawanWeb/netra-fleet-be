package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/geo"
)

// GeoSite is the georeference of a gps deployment (one row): its control points, the transform
// fitted from them, the basemap and an optional overlay image.
type GeoSite struct {
	Points     []geo.ControlPoint
	HeadingDeg *float64
	Fit        *GeoFit // nil: not georeferenced
	Basemap    Basemap
	Overlay    *GeoOverlay
	UpdatedBy  string
	UpdatedAt  *time.Time // nil until the site was first saved
}

// GeoFit is a stored transform with its method and RMS residual.
type GeoFit struct {
	geo.Transform
	Method string
	RMS    float64
}

// Basemap is the tile layer under the map: osm, satellite, custom (URL template) or none.
type Basemap struct {
	Kind        string
	URL         string
	Attribution string
	MaxZoom     int
}

// GeoOverlay is an image of the site (north up) over its WGS84 bounds.
type GeoOverlay struct {
	Path        string
	ContentType string
	North       float64
	South       float64
	East        float64
	West        float64
	Opacity     float64
	UpdatedAt   time.Time
}

// DefaultBasemap is the basemap of a site that has not been saved yet.
var DefaultBasemap = Basemap{Kind: "osm", MaxZoom: 19}

// GeoSite returns the site, or an empty one with the default basemap before it was first saved.
func (s *Store) GeoSite(ctx context.Context) (GeoSite, error) {
	g := GeoSite{Points: []geo.ControlPoint{}, Basemap: DefaultBasemap}
	var points []byte
	var method, otype, opath sql.NullString
	var refLat, refLon, scale, rot, east, north, rms sql.NullFloat64
	var oNorth, oSouth, oEast, oWest sql.NullFloat64
	var opacity float64
	var oUpdated sql.NullTime
	var updated time.Time
	err := s.db.QueryRowContext(ctx, `SELECT control_points, heading_deg, fit_method, ref_lat, ref_lon, scale,
		rotation_rad, east_m, north_m, rms_m, basemap, basemap_url, basemap_attribution, basemap_max_zoom,
		overlay_path, overlay_type, overlay_north, overlay_south, overlay_east, overlay_west, overlay_opacity,
		overlay_updated_at, updated_by, updated_at FROM geo_site WHERE id = 1`).Scan(
		&points, &g.HeadingDeg, &method, &refLat, &refLon, &scale, &rot, &east, &north, &rms,
		&g.Basemap.Kind, &g.Basemap.URL, &g.Basemap.Attribution, &g.Basemap.MaxZoom,
		&opath, &otype, &oNorth, &oSouth, &oEast, &oWest, &opacity, &oUpdated, &g.UpdatedBy, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return g, nil
	}
	if err != nil {
		return g, err
	}
	g.UpdatedAt = &updated
	if err := json.Unmarshal(points, &g.Points); err != nil {
		return g, err
	}
	if method.Valid && refLat.Valid && refLon.Valid && scale.Valid && rot.Valid && east.Valid && north.Valid {
		g.Fit = &GeoFit{Method: method.String, RMS: rms.Float64, Transform: geo.Transform{RefLat: refLat.Float64,
			RefLon: refLon.Float64, Scale: scale.Float64, Rotation: rot.Float64, East: east.Float64, North: north.Float64}}
	}
	if opath.Valid && oNorth.Valid && oSouth.Valid && oEast.Valid && oWest.Valid {
		g.Overlay = &GeoOverlay{Path: opath.String, ContentType: otype.String, North: oNorth.Float64,
			South: oSouth.Float64, East: oEast.Float64, West: oWest.Float64, Opacity: opacity, UpdatedAt: oUpdated.Time}
	}
	return g, nil
}

// SaveGeoSite stores the control points, heading, fit and basemap (the overlay is kept).
func (s *Store) SaveGeoSite(ctx context.Context, g GeoSite, user string) error {
	points, err := json.Marshal(g.Points)
	if err != nil {
		return err
	}
	var method *string
	var refLat, refLon, scale, rot, east, north, rms *float64
	if f := g.Fit; f != nil {
		method, refLat, refLon, scale, rot, east, north, rms = &f.Method, &f.RefLat, &f.RefLon, &f.Scale, &f.Rotation,
			&f.East, &f.North, &f.RMS
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO geo_site (id, control_points, heading_deg, fit_method, ref_lat, ref_lon,
		scale, rotation_rad, east_m, north_m, rms_m, basemap, basemap_url, basemap_attribution, basemap_max_zoom,
		updated_by, updated_at) VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, now())
		ON CONFLICT (id) DO UPDATE SET control_points = EXCLUDED.control_points, heading_deg = EXCLUDED.heading_deg,
		fit_method = EXCLUDED.fit_method, ref_lat = EXCLUDED.ref_lat, ref_lon = EXCLUDED.ref_lon,
		scale = EXCLUDED.scale, rotation_rad = EXCLUDED.rotation_rad, east_m = EXCLUDED.east_m,
		north_m = EXCLUDED.north_m, rms_m = EXCLUDED.rms_m, basemap = EXCLUDED.basemap,
		basemap_url = EXCLUDED.basemap_url, basemap_attribution = EXCLUDED.basemap_attribution,
		basemap_max_zoom = EXCLUDED.basemap_max_zoom, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		points, g.HeadingDeg, method, refLat, refLon, scale, rot, east, north, rms, g.Basemap.Kind, g.Basemap.URL,
		g.Basemap.Attribution, g.Basemap.MaxZoom, user)
	return err
}

// SetGeoOverlay stores the overlay (nil removes it) and returns the file of the previous one, ""
// when there was none or it is the same file.
func (s *Store) SetGeoOverlay(ctx context.Context, o *GeoOverlay, user string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO geo_site (id) VALUES (1) ON CONFLICT (id) DO NOTHING`); err != nil {
		return "", err
	}
	var old sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT overlay_path FROM geo_site WHERE id = 1 FOR UPDATE`).Scan(&old); err != nil {
		return "", err
	}
	if o == nil {
		_, err = tx.ExecContext(ctx, `UPDATE geo_site SET overlay_path = NULL, overlay_type = NULL, overlay_north = NULL,
			overlay_south = NULL, overlay_east = NULL, overlay_west = NULL, overlay_opacity = 1, overlay_updated_at = NULL,
			updated_by = $1, updated_at = now() WHERE id = 1`, user)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE geo_site SET overlay_path = $1, overlay_type = $2, overlay_north = $3,
			overlay_south = $4, overlay_east = $5, overlay_west = $6, overlay_opacity = $7, overlay_updated_at = now(),
			updated_by = $8, updated_at = now() WHERE id = 1`,
			o.Path, o.ContentType, o.North, o.South, o.East, o.West, o.Opacity, user)
	}
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	if old.Valid && (o == nil || o.Path != old.String) {
		return old.String, nil
	}
	return "", nil
}

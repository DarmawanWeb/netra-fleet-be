package ext

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
)

// Statuses an anomaly goes through: queued -> analyzing -> needs_validation | confirmed | dismissed,
// then an operator moves needs_validation to confirmed or rejected.
var Statuses = []string{"queued", "analyzing", "needs_validation", "confirmed", "rejected", "dismissed"}

const (
	defaultLimit = 50
	maxLimit     = 200
)

// Detection is one classification of the photo.
type Detection struct {
	Category    string  `json:"category"`
	Confidence  float64 `json:"confidence"`
	Description string  `json:"description"`
}

// Validation is the operator's decision.
type Validation struct {
	Result      string    `json:"result"` // confirmed | rejected
	ValidatedAt time.Time `json:"validated_at"`
}

// Position is where the robot was, in the robot map frame (metres, radians).
type Position struct {
	X     float64  `json:"x"`
	Y     float64  `json:"y"`
	Yaw   *float64 `json:"yaw"`
	Frame string   `json:"frame"`
}

// Ref names a robot.
type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Anomaly is the integration representation of a detection.
type Anomaly struct {
	ID             int64       `json:"id"`
	UID            string      `json:"uid"`
	Status         string      `json:"status"`
	Category       string      `json:"category"`
	DetectedAt     time.Time   `json:"detected_at"`
	UpdatedAt      time.Time   `json:"updated_at"`
	Robot          Ref         `json:"robot"`
	PatrolID       *int64      `json:"patrol_id"`
	RouteID        *int64      `json:"route_id"`
	StopIndex      *int64      `json:"stop_index"`
	Position       *Position   `json:"position"`
	EarlyDetection Detection   `json:"early_detection"`
	FinalDetection *Detection  `json:"final_detection"`
	Validation     *Validation `json:"validation"`
	ImageURL       string      `json:"image_url"`
}

// AnomalyPage is one page of the change feed.
type AnomalyPage struct {
	Data       []Anomaly `json:"data"`
	NextCursor string    `json:"next_cursor"`
	HasMore    bool      `json:"has_more"`
}

func toAnomaly(f store.FeedAnomaly, base string) Anomaly {
	out := Anomaly{
		ID: f.ID, UID: f.UID, Status: f.Status, Category: f.EarlyCategory, DetectedAt: f.DetectedAt.UTC(),
		UpdatedAt: f.UpdatedAt.UTC(), Robot: Ref{ID: f.RobotID, Name: f.RobotName}, PatrolID: f.PatrolID,
		RouteID: f.RouteID, StopIndex: f.StopIndex,
		EarlyDetection: Detection{Category: f.EarlyCategory, Confidence: f.EarlyConfidence, Description: f.EarlyNote},
		ImageURL:       fmt.Sprintf("%s%s/anomalies/%d/image", base, Prefix, f.ID),
	}
	if f.FinalCategory != nil {
		out.Category = *f.FinalCategory
		d := Detection{Category: *f.FinalCategory, Description: f.FinalNote}
		if f.FinalConfidence != nil {
			d.Confidence = *f.FinalConfidence
		}
		out.FinalDetection = &d
	}
	if f.ValidatedAt != nil && (f.Status == "confirmed" || f.Status == "rejected") {
		out.Validation = &Validation{Result: f.Status, ValidatedAt: f.ValidatedAt.UTC()}
	}
	if f.X != nil && f.Y != nil {
		out.Position = &Position{X: *f.X, Y: *f.Y, Yaw: f.Yaw, Frame: "map"}
	}
	return out
}

// Cursor is a position in the change feed: after this change time, then after this id.
type Cursor struct {
	Updated time.Time
	ID      int64
}

// EncodeCursor makes the opaque next_cursor string.
func EncodeCursor(c Cursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%d", c.Updated.UnixMicro(), c.ID)))
}

// DecodeCursor reads a next_cursor string.
func DecodeCursor(s string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, errors.New("cursor is not valid")
	}
	micros, id, ok := strings.Cut(string(raw), ":")
	us, err1 := strconv.ParseInt(micros, 10, 64)
	n, err2 := strconv.ParseInt(id, 10, 64)
	if !ok || err1 != nil || err2 != nil || n < 0 {
		return Cursor{}, errors.New("cursor is not valid")
	}
	return Cursor{Updated: time.UnixMicro(us), ID: n}, nil
}

func csvParam(q url.Values, key string) []string {
	var out []string
	for _, v := range q[key] {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func boolParam(q url.Values, key string) (*bool, error) {
	v := q.Get(key)
	if v == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return nil, fmt.Errorf("%s must be true or false", key)
	}
	return &b, nil
}

func timeParam(q url.Values, key string) (*time.Time, error) {
	v := q.Get(key)
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return nil, fmt.Errorf("%s must be an RFC 3339 time such as 2026-10-16T08:00:00+07:00", key)
	}
	return &t, nil
}

func limitParam(q url.Values) (int, error) {
	v := q.Get("limit")
	if v == "" {
		return defaultLimit, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > maxLimit {
		return 0, fmt.Errorf("limit must be 1 to %d", maxLimit)
	}
	return n, nil
}

// feedFilter reads the list parameters; the error text goes back to the client.
func feedFilter(q url.Values) (store.AnomalyFeedFilter, error) {
	var f store.AnomalyFeedFilter
	var err error
	f.Statuses = csvParam(q, "status")
	for _, s := range f.Statuses {
		if !slices.Contains(Statuses, s) {
			return f, fmt.Errorf("unknown status %q (one of %s)", s, strings.Join(Statuses, ", "))
		}
	}
	f.Categories = csvParam(q, "category")
	f.RobotID = q.Get("robot_id")
	if f.Final, err = boolParam(q, "final"); err != nil {
		return f, err
	}
	if f.Validated, err = boolParam(q, "validated"); err != nil {
		return f, err
	}
	if f.DetectedFrom, err = timeParam(q, "detected_from"); err != nil {
		return f, err
	}
	if f.DetectedTo, err = timeParam(q, "detected_to"); err != nil {
		return f, err
	}
	if f.Limit, err = limitParam(q); err != nil {
		return f, err
	}
	since, err := timeParam(q, "updated_since")
	if err != nil {
		return f, err
	}
	if c := q.Get("cursor"); c != "" {
		if since != nil {
			return f, errors.New("give either cursor or updated_since, not both")
		}
		cur, err := DecodeCursor(c)
		if err != nil {
			return f, err
		}
		f.AfterUpdated, f.AfterID = cur.Updated, cur.ID
	} else if since != nil {
		// Changes at or after updated_since: start just before it.
		f.AfterUpdated, f.AfterID = since.Add(-time.Microsecond), 1<<62
	}
	return f, nil
}

// listAnomalies is the change feed: anomalies ordered by their last change, oldest first.
func (a *API) listAnomalies(w http.ResponseWriter, r *http.Request) {
	f, err := feedFilter(r.URL.Query())
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	limit := f.Limit
	f.Limit = limit + 1 // one more to know whether there is a next page
	rows, err := a.store.AnomalyFeed(r.Context(), f)
	if err != nil {
		fail(w, err)
		return
	}
	page := AnomalyPage{Data: []Anomaly{}, HasMore: len(rows) > limit}
	if page.HasMore {
		rows = rows[:limit]
	}
	base := baseURL(r)
	next := Cursor{Updated: f.AfterUpdated, ID: f.AfterID}
	for _, row := range rows {
		page.Data = append(page.Data, toAnomaly(row, base))
		next = Cursor{Updated: row.UpdatedAt, ID: row.ID}
	}
	page.NextCursor = EncodeCursor(next)
	writeJSON(w, http.StatusOK, page)
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func (a *API) getAnomaly(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no such item")
		return
	}
	row, err := a.store.FeedAnomalyByID(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toAnomaly(row, baseURL(r)))
}

// anomalyImage sends the original JPEG the robot uploaded.
func (a *API) anomalyImage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no such item")
		return
	}
	row, err := a.store.FeedAnomalyByID(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	f, err := os.Open(row.ImagePath)
	if err != nil {
		writeErr(w, http.StatusNotFound, "not_found", "the photo of this anomaly is no longer stored")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="netra-anomaly-%d.jpg"`, id))
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// Category is an anomaly category with its display label.
type Category struct {
	Category string `json:"category"`
	Label    string `json:"label"`
}

func (a *API) categories(w http.ResponseWriter, r *http.Request) {
	rules, err := a.store.ListRules(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	out := make([]Category, 0, len(rules))
	for _, rule := range rules {
		out = append(out, Category{Category: rule.Category, Label: rule.Label})
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": out})
}

package licensemgr

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
)

// licenseView is a license as the UI shows it.
type licenseView struct {
	ID           string          `json:"id"`
	ClientID     string          `json:"client_id"`
	ClientName   string          `json:"client_name"` // the client's current name; the payload has the signed one
	DeploymentID string          `json:"deployment_id,omitempty"`
	Deployment   string          `json:"deployment,omitempty"`
	Binding      string          `json:"deployment_installation,omitempty"` // the deployment's ID now; a renewal binds to it
	License      license.License `json:"license"`
	HasLogo      bool            `json:"has_logo"`
	Status       license.Status  `json:"status"`
	Expiring     bool            `json:"expiring"` // valid, and expires within Expiring
	GraceEnds    *time.Time      `json:"grace_ends,omitempty"`
	IssuedBy     string          `json:"issued_by"`
	Note         string          `json:"note,omitempty"`
	Kind         string          `json:"kind"`
	Key          string          `json:"key"`
	Supersedes   string          `json:"supersedes,omitempty"`
	SupersededBy string          `json:"superseded_by,omitempty"`
	FileName     string          `json:"file_name"`
	Text         string          `json:"text,omitempty"` // the detail only
}

func (s *Server) view(r Record, replaced map[string]string, clientName string) licenseView {
	now := s.now()
	v := licenseView{
		ID: r.ID, ClientID: r.ClientID, ClientName: clientName, DeploymentID: r.DeploymentID, License: r.License,
		HasLogo: s.store.HasLogo(r.ID) || r.License.Branding.Logo != "", Status: r.License.StatusAt(now),
		IssuedBy: r.IssuedBy, Note: r.Note, Kind: r.Kind, Key: r.Key, Supersedes: r.Supersedes,
		SupersededBy: replaced[r.ID], FileName: FileName(r.License), Text: r.Text,
	}
	if exp := r.License.ExpiresAt; !exp.IsZero() {
		grace := exp.Add(license.Grace)
		v.GraceEnds = &grace
		v.Expiring = v.Status == license.Valid && exp.Sub(now) <= Expiring
	}
	if v.ClientName == "" {
		v.ClientName = r.License.Client
	}
	if c, ok := s.store.Client(r.ClientID); ok {
		v.ClientName = c.Name
		if d, ok := c.Deployment(r.DeploymentID); ok {
			v.Deployment, v.Binding = d.Name, d.Installation
		}
	}
	return v
}

// matches is the filter of the license list: status (valid, expiring, grace, expired), client,
// edition, and current (not replaced by a newer license).
func matches(v licenseView, q map[string]string) bool {
	switch q["status"] {
	case "valid":
		if v.Status != license.Valid {
			return false
		}
	case "expiring":
		if !v.Expiring {
			return false
		}
	case "grace", "expired":
		if string(v.Status) != q["status"] {
			return false
		}
	}
	return (q["client"] == "" || q["client"] == v.ClientID) &&
		(q["edition"] == "" || q["edition"] == v.License.Edition) &&
		(q["current"] == "" || v.SupersededBy == "")
}

func (s *Server) listLicenses(w http.ResponseWriter, r *http.Request, _ string) {
	q := map[string]string{}
	for _, k := range []string{"status", "client", "edition", "current"} {
		q[k] = r.URL.Query().Get(k)
	}
	if q["current"] == "0" || q["current"] == "false" {
		q["current"] = ""
	}
	replaced := s.store.SupersededBy()
	out := []licenseView{}
	for _, rec := range s.store.Licenses() {
		if v := s.view(rec, replaced, ""); matches(v, q) {
			out = append(out, v)
		}
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) getLicense(w http.ResponseWriter, r *http.Request, _ string) {
	rec, err := s.store.License(r.PathValue("id"))
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, s.view(rec, s.store.SupersededBy(), ""))
}

// licenseFile downloads the license as <client-slug>-<YYYYMMDD>.lic.
func (s *Server) licenseFile(w http.ResponseWriter, r *http.Request, _ string) {
	rec, err := s.store.License(r.PathValue("id"))
	if err != nil {
		replyErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": FileName(rec.License)}))
	_, _ = w.Write([]byte(rec.Text + "\n"))
}

// sign signs l, checks the text against the key file and keeps the record.
func (s *Server) sign(l license.License, rec Record) (Record, error) {
	kf, err := s.store.KeyFile()
	if err != nil {
		return rec, err
	}
	if kf == nil {
		return rec, ErrNoKey
	}
	pub, err := kf.Public()
	if err != nil {
		return rec, err
	}
	text, err := s.keys.Sign(l)
	if err != nil {
		return rec, err
	}
	if _, err := license.Verify(text, []ed25519.PublicKey{pub}); err != nil {
		return rec, errors.New("the unlocked key does not match key.enc: " + err.Error())
	}
	rec.ID, rec.License, rec.Text, rec.Key = l.ID, l, text, Fingerprint(pub)
	return rec, s.store.SaveLicense(rec)
}

func (s *Server) issue(w http.ResponseWriter, r *http.Request, user string) {
	var req IssueRequest
	if err := decode(w, r, &req); err != nil {
		replyErr(w, err)
		return
	}
	s.edit.Lock()
	defer s.edit.Unlock()
	c, ok := s.store.Client(req.ClientID)
	if !ok {
		replyErr(w, fieldErr("client_id", "choose a client"))
		return
	}
	kind := KindIssue
	if req.Supersedes != "" {
		old, err := s.store.License(req.Supersedes)
		if err != nil {
			replyErr(w, fieldErr("supersedes", "the license to replace does not exist"))
			return
		}
		if old.ClientID != c.ID {
			replyErr(w, fieldErr("supersedes", "the license to replace belongs to another client"))
			return
		}
		kind = KindChange
	}
	l, err := buildLicense(req, c, s.now())
	if err != nil {
		replyErr(w, err)
		return
	}
	rec, err := s.sign(l, Record{ClientID: c.ID, DeploymentID: req.DeploymentID, IssuedBy: user,
		Note: cleanText(req.Note, maxNote), Kind: kind, Supersedes: req.Supersedes})
	if err != nil {
		replyErr(w, err)
		return
	}
	s.record(r, user, "license."+kind, rec.ID, licenseDetail(rec))
	reply(w, http.StatusCreated, s.view(rec, s.store.SupersededBy(), c.Name))
}

func licenseDetail(rec Record) map[string]any {
	l := rec.License
	d := map[string]any{"client": l.Client, "edition": l.Edition, "tier": l.Tier, "max_robots": l.MaxRobots}
	if l.Installation != "" {
		d["installation"] = l.Installation
	}
	if !l.ExpiresAt.IsZero() {
		d["expires_at"] = l.ExpiresAt
	}
	if rec.Supersedes != "" {
		d["supersedes"] = rec.Supersedes
	}
	return d
}

// renew issues the same terms with new dates; a bound license follows its deployment's current
// installation ID.
func (s *Server) renew(w http.ResponseWriter, r *http.Request, user string) {
	var body struct {
		Validity Validity `json:"validity"`
		Note     string   `json:"note"`
	}
	if err := decode(w, r, &body); err != nil {
		replyErr(w, err)
		return
	}
	s.edit.Lock()
	defer s.edit.Unlock()
	old, err := s.store.License(r.PathValue("id"))
	if err != nil {
		replyErr(w, err)
		return
	}
	c, ok := s.store.Client(old.ClientID)
	if !ok {
		replyErr(w, errors.New("the client of the license is gone"))
		return
	}
	installation := old.License.Installation
	if d, ok := c.Deployment(old.DeploymentID); ok {
		installation = d.Installation
	}
	l, err := renewal(old.License, c, installation, body.Validity, s.now())
	if err != nil {
		replyErr(w, err)
		return
	}
	rec, err := s.sign(l, Record{ClientID: c.ID, DeploymentID: old.DeploymentID, IssuedBy: user,
		Note: cleanText(body.Note, maxNote), Kind: KindRenew, Supersedes: old.ID})
	if err != nil {
		replyErr(w, err)
		return
	}
	s.record(r, user, "license.renew", rec.ID, licenseDetail(rec))
	reply(w, http.StatusCreated, s.view(rec, s.store.SupersededBy(), c.Name))
}

type signatureCheck struct {
	OK    bool   `json:"ok"`
	Keys  int    `json:"keys"`
	Error string `json:"error,omitempty"`
}

type verifyView struct {
	Error        string           `json:"error,omitempty"`   // not a license at all
	Warning      string           `json:"warning,omitempty"` // a fixable problem of the text
	License      *license.License `json:"license,omitempty"` // the payload, decoded also when no key signed it
	Manager      signatureCheck   `json:"manager"`           // the manager's key
	BuiltIn      signatureCheck   `json:"built_in"`          // the keys built into this build of netra-api
	Status       license.Status   `json:"status,omitempty"`
	Expiring     bool             `json:"expiring"`
	GraceEnds    *time.Time       `json:"grace_ends,omitempty"`
	Installation *installCheck    `json:"installation,omitempty"`
	Record       *recordRef       `json:"record,omitempty"`
}

type installCheck struct {
	ID    string `json:"id"`
	Bound bool   `json:"bound"`
	Match bool   `json:"match"`
}

type recordRef struct {
	ID       string `json:"id"`
	SameText bool   `json:"same_text"`
}

// decodeText reads the payload of a license text without checking its signature.
func decodeText(text string) (license.License, error) {
	var l license.License
	rest, ok := strings.CutPrefix(text, license.Prefix)
	if !ok {
		return l, errors.New("not a NETRA license (no " + license.Prefix + " prefix)")
	}
	body, sig, ok := strings.Cut(rest, ".")
	if !ok || sig == "" {
		return l, errors.New("the license has no signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return l, errors.New("the payload is damaged (not base64url)")
	}
	if err := json.Unmarshal(payload, &l); err != nil {
		return l, errors.New("the payload is damaged (not license JSON)")
	}
	return l, nil
}

func checkSignature(text string, keys []ed25519.PublicKey) signatureCheck {
	c := signatureCheck{Keys: len(keys)}
	if len(keys) == 0 {
		c.Error = "no key"
		return c
	}
	if _, err := license.Verify(text, keys); err != nil {
		c.Error = err.Error()
		return c
	}
	c.OK = true
	return c
}

// verify checks any license text the way netra-api does, against the manager's key and the keys
// built into this build.
func (s *Server) verify(w http.ResponseWriter, r *http.Request, _ string) {
	var body struct {
		Text         string `json:"text"`
		Installation string `json:"installation"`
	}
	if err := decode(w, r, &body); err != nil {
		replyErr(w, err)
		return
	}
	var v verifyView
	text := strings.TrimSpace(body.Text)
	if strings.ContainsAny(text, " \t\r\n") {
		text = strings.Join(strings.Fields(text), "")
		v.Warning = "the text has line breaks or spaces inside (a mail program may have wrapped it): netra-api " +
			"refuses it as is; send the .lic file, or join the text into one line"
	}
	l, err := decodeText(text)
	if err != nil {
		v.Error = err.Error()
		reply(w, http.StatusOK, v)
		return
	}
	v.License = &l
	if kf, err := s.store.KeyFile(); err == nil && kf != nil {
		if pub, err := kf.Public(); err == nil {
			v.Manager = checkSignature(text, []ed25519.PublicKey{pub})
		}
	} else {
		v.Manager.Error = "the manager has no key yet"
	}
	trusted, err := s.trusted()
	v.BuiltIn = checkSignature(text, trusted)
	if err != nil {
		v.BuiltIn.Error = err.Error()
	}
	now := s.now()
	v.Status = l.StatusAt(now)
	if !l.ExpiresAt.IsZero() {
		grace := l.ExpiresAt.Add(license.Grace)
		v.GraceEnds = &grace
		v.Expiring = v.Status == license.Valid && l.ExpiresAt.Sub(now) <= Expiring
	}
	if id := strings.TrimSpace(body.Installation); id != "" {
		n := license.NormalizeInstallation(id)
		v.Installation = &installCheck{ID: n, Bound: l.Installation != "",
			Match: l.Installation == "" || license.NormalizeInstallation(l.Installation) == n}
	}
	if rec, err := s.store.License(l.ID); err == nil {
		v.Record = &recordRef{ID: rec.ID, SameText: rec.Text == text}
	}
	reply(w, http.StatusOK, v)
}

type catalogEntry struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// meta is what the forms need: modules, editions, tiers, map modes and limits.
func (s *Server) meta(w http.ResponseWriter, _ *http.Request, _ string) {
	catalog := make([]catalogEntry, 0, len(license.Catalog))
	for _, c := range license.Catalog {
		catalog = append(catalog, catalogEntry{c.ID, c.Description})
	}
	reply(w, http.StatusOK, map[string]any{
		"product": license.Product, "catalog": catalog, "editions": license.Editions, "tiers": license.Tiers,
		"map_modes": license.MapModes, "edition_maps": license.EditionMaps, "grace_days": int(license.Grace.Hours() / 24),
		"expiring_days": int(Expiring.Hours() / 24), "max_logo": MaxLogo, "max_robots": MaxRobots, "max_days": MaxDays,
		"min_passphrase": MinPassphrase, "min_password": minPassword,
	})
}

type counts struct {
	Clients     int `json:"clients"`
	Deployments int `json:"deployments"`
	Licenses    int `json:"licenses"` // issued
	Current     int `json:"current"`  // not replaced
	Valid       int `json:"valid"`    // current, valid and not expiring
	Expiring    int `json:"expiring"`
	Grace       int `json:"grace"`
	Expired     int `json:"expired"`
	Unbound     int `json:"unbound"` // current, in force and bound to no installation
}

func (s *Server) dashboard(w http.ResponseWriter, _ *http.Request, _ string) {
	var n counts
	clients := s.store.Clients()
	n.Clients = len(clients)
	for _, c := range clients {
		n.Deployments += len(c.Deployments)
	}
	replaced := s.store.SupersededBy()
	attention := []licenseView{}
	for _, rec := range s.store.Licenses() {
		n.Licenses++
		if replaced[rec.ID] != "" {
			continue
		}
		n.Current++
		v := s.view(rec, replaced, "")
		switch {
		case v.Expiring:
			n.Expiring++
			attention = append(attention, v)
		case v.Status == license.Valid:
			n.Valid++
		case v.Status == license.InGrace:
			n.Grace++
			attention = append(attention, v)
		default:
			n.Expired++
		}
		if v.Status != license.Expired && rec.License.Installation == "" {
			n.Unbound++
		}
	}
	sort.Slice(attention, func(i, j int) bool { return attention[i].License.ExpiresAt.Before(attention[j].License.ExpiresAt) })
	entries, err := s.store.AuditLog(300)
	if err != nil {
		replyErr(w, err)
		return
	}
	// What was done; sign-ins and sign-outs stay in the audit log.
	recent := []AuditEntry{}
	for _, e := range entries {
		if e.Action != "login" && e.Action != "logout" && len(recent) < 12 {
			recent = append(recent, e)
		}
	}
	key, err := s.keyView()
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, map[string]any{"counts": n, "attention": attention, "recent": recent, "key": key})
}

func (s *Server) auditLog(w http.ResponseWriter, r *http.Request, _ string) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = 300
	}
	entries, err := s.store.AuditLog(min(limit, 2000))
	if err != nil {
		replyErr(w, err)
		return
	}
	reply(w, http.StatusOK, entries)
}

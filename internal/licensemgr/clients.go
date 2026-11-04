package licensemgr

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DarmawanWeb/netra-fleet-be/internal/license"
)

// Lengths of the free texts.
const (
	maxName    = 120
	maxContact = 300
	maxNote    = 2000
)

// cleanText trims a free text and limits its length.
func cleanText(s string, limit int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > limit {
		s = string([]rune(s)[:limit])
	}
	return s
}

type clientBody struct {
	Name    string `json:"name"`
	Contact string `json:"contact"`
	Notes   string `json:"notes"`
}

func (b *clientBody) check() error {
	b.Name, b.Contact, b.Notes = cleanText(b.Name, maxName), cleanText(b.Contact, maxContact), cleanText(b.Notes, maxNote)
	if b.Name == "" {
		return fieldErr("name", "the client needs a name")
	}
	return nil
}

// deploymentView is a deployment with the newest license bound to it.
type deploymentView struct {
	Deployment
	License *licenseView `json:"license,omitempty"`
}

type clientView struct {
	Client
	Deployments []deploymentView `json:"deployments"`
	Licenses    int              `json:"licenses"`    // issued
	Active      int              `json:"active"`      // in force (valid or in grace) and not replaced
	NextExpiry  *time.Time       `json:"next_expiry"` // the first expiry among the licenses in force
	Issued      []licenseView    `json:"issued,omitempty"`
}

func (s *Server) viewClient(c Client, withLicenses bool) clientView {
	now := s.now()
	replaced := s.store.SupersededBy()
	v := clientView{Client: c, Deployments: make([]deploymentView, 0, len(c.Deployments))}
	latest := map[string]Record{} // newest current license per deployment
	for _, r := range s.store.Licenses() {
		if r.ClientID != c.ID {
			continue
		}
		v.Licenses++
		if withLicenses {
			v.Issued = append(v.Issued, s.view(r, replaced, c.Name))
		}
		if replaced[r.ID] != "" {
			continue
		}
		if st := r.License.StatusAt(now); st == license.Valid || st == license.InGrace {
			v.Active++
			if exp := r.License.ExpiresAt; !exp.IsZero() && (v.NextExpiry == nil || exp.Before(*v.NextExpiry)) {
				v.NextExpiry = &exp
			}
		}
		if r.DeploymentID != "" {
			if prev, ok := latest[r.DeploymentID]; !ok || prev.License.IssuedAt.Before(r.License.IssuedAt) {
				latest[r.DeploymentID] = r
			}
		}
	}
	for _, d := range c.Deployments {
		dv := deploymentView{Deployment: d}
		if r, ok := latest[d.ID]; ok {
			lv := s.view(r, replaced, c.Name)
			dv.License = &lv
		}
		v.Deployments = append(v.Deployments, dv)
	}
	return v
}

func (s *Server) listClients(w http.ResponseWriter, _ *http.Request, _ string) {
	clients := s.store.Clients()
	out := make([]clientView, 0, len(clients))
	for _, c := range clients {
		out = append(out, s.viewClient(c, false))
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) getClient(w http.ResponseWriter, r *http.Request, _ string) {
	c, ok := s.store.Client(r.PathValue("id"))
	if !ok {
		replyErr(w, ErrNotFound)
		return
	}
	reply(w, http.StatusOK, s.viewClient(c, true))
}

func (s *Server) createClient(w http.ResponseWriter, r *http.Request, user string) {
	var body clientBody
	if err := decode(w, r, &body); err != nil {
		replyErr(w, err)
		return
	}
	if err := body.check(); err != nil {
		replyErr(w, err)
		return
	}
	s.edit.Lock()
	defer s.edit.Unlock()
	now := s.now().UTC()
	c, err := s.store.SaveClient(Client{Name: body.Name, Contact: body.Contact, Notes: body.Notes, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		replyErr(w, err)
		return
	}
	s.record(r, user, "client.create", c.ID, map[string]any{"name": c.Name})
	reply(w, http.StatusCreated, s.viewClient(c, false))
}

func (s *Server) updateClient(w http.ResponseWriter, r *http.Request, user string) {
	var body clientBody
	if err := decode(w, r, &body); err != nil {
		replyErr(w, err)
		return
	}
	if err := body.check(); err != nil {
		replyErr(w, err)
		return
	}
	s.edit.Lock()
	defer s.edit.Unlock()
	c, ok := s.store.Client(r.PathValue("id"))
	if !ok {
		replyErr(w, ErrNotFound)
		return
	}
	detail := map[string]any{"name": body.Name}
	if c.Name != body.Name {
		detail["was"] = c.Name
	}
	c.Name, c.Contact, c.Notes, c.UpdatedAt = body.Name, body.Contact, body.Notes, s.now().UTC()
	c, err := s.store.SaveClient(c)
	if err != nil {
		replyErr(w, err)
		return
	}
	s.record(r, user, "client.update", c.ID, detail)
	reply(w, http.StatusOK, s.viewClient(c, false))
}

func (s *Server) deleteClient(w http.ResponseWriter, r *http.Request, user string) {
	s.edit.Lock()
	defer s.edit.Unlock()
	c, ok := s.store.Client(r.PathValue("id"))
	if !ok {
		replyErr(w, ErrNotFound)
		return
	}
	if err := s.store.DeleteClient(c.ID); err != nil {
		replyError(w, http.StatusConflict, err.Error())
		return
	}
	s.record(r, user, "client.delete", c.ID, map[string]any{"name": c.Name})
	w.WriteHeader(http.StatusNoContent)
}

type deploymentBody struct {
	Name         string `json:"name"`
	Installation string `json:"installation"`
	Notes        string `json:"notes"`
}

// saveDeployment adds a deployment (POST) or changes one (PUT .../{dep}).
func (s *Server) saveDeployment(w http.ResponseWriter, r *http.Request, user string) {
	var body deploymentBody
	if err := decode(w, r, &body); err != nil {
		replyErr(w, err)
		return
	}
	body.Name, body.Notes = cleanText(body.Name, maxName), cleanText(body.Notes, maxNote)
	if body.Name == "" {
		replyErr(w, fieldErr("name", "the deployment needs a name, e.g. the site"))
		return
	}
	id, err := normalizeInstallation(body.Installation)
	if err != nil {
		replyErr(w, fieldErr("installation", "%v", err))
		return
	}
	s.edit.Lock()
	defer s.edit.Unlock()
	c, ok := s.store.Client(r.PathValue("id"))
	if !ok {
		replyErr(w, ErrNotFound)
		return
	}
	depID := r.PathValue("dep")
	if id != "" {
		if oc, od, taken := s.store.Installation(id); taken && od.ID != depID {
			replyErr(w, fieldErr("installation", "%s is already the deployment %s of %s", id, od.Name, oc.Name))
			return
		}
	}
	now := s.now().UTC()
	d := Deployment{ID: depID, Name: body.Name, Installation: id, Notes: body.Notes, CreatedAt: now, UpdatedAt: now}
	action, detail := "deployment.create", map[string]any{"client": c.Name, "name": d.Name, "installation": id}
	if depID == "" {
		c.Deployments = append(c.Deployments, d)
	} else {
		i := deploymentIndex(c, depID)
		if i < 0 {
			replyErr(w, ErrNotFound)
			return
		}
		action, d.CreatedAt = "deployment.update", c.Deployments[i].CreatedAt
		if was := c.Deployments[i].Installation; was != id {
			detail["was"] = was
		}
		c.Deployments[i] = d
	}
	c.UpdatedAt = now
	if c, err = s.store.SaveClient(c); err != nil {
		replyErr(w, err)
		return
	}
	if depID == "" {
		d = c.Deployments[len(c.Deployments)-1]
	}
	s.record(r, user, action, d.ID, detail)
	status := http.StatusOK
	if action == "deployment.create" {
		status = http.StatusCreated
	}
	reply(w, status, d)
}

func deploymentIndex(c Client, id string) int {
	for i, d := range c.Deployments {
		if d.ID == id {
			return i
		}
	}
	return -1
}

func (s *Server) deleteDeployment(w http.ResponseWriter, r *http.Request, user string) {
	s.edit.Lock()
	defer s.edit.Unlock()
	c, ok := s.store.Client(r.PathValue("id"))
	if !ok {
		replyErr(w, ErrNotFound)
		return
	}
	i := deploymentIndex(c, r.PathValue("dep"))
	if i < 0 {
		replyErr(w, ErrNotFound)
		return
	}
	d := c.Deployments[i]
	for _, l := range s.store.Licenses() {
		if l.DeploymentID == d.ID {
			replyError(w, http.StatusConflict, "the deployment has licenses; they are the record of what was sold")
			return
		}
	}
	c.Deployments = append(c.Deployments[:i:i], c.Deployments[i+1:]...)
	c.UpdatedAt = s.now().UTC()
	if _, err := s.store.SaveClient(c); err != nil {
		replyErr(w, err)
		return
	}
	s.record(r, user, "deployment.delete", d.ID, map[string]any{"client": c.Name, "name": d.Name, "installation": d.Installation})
	w.WriteHeader(http.StatusNoContent)
}

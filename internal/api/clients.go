package api

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"slices"
	"strings"

	"github.com/DarmawanWeb/netra-fleet-be/internal/auth"
	"github.com/DarmawanWeb/netra-fleet-be/internal/ext"
	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
)

// Integration clients (third-party applications on the /api/ext/v1 API), managed by administrators.

const secretPrefix = "ncs_"

func newClientID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "nc_" + hex.EncodeToString(b)
}

func newClientSecret() string { return secretPrefix + auth.NewToken() }

// cleanScopes drops duplicates and keeps the canonical order; ok is false for an unknown scope.
func cleanScopes(in []string) ([]string, bool) {
	if !ext.ValidScopes(in) {
		return nil, false
	}
	out := []string{}
	for _, s := range ext.Scopes {
		if slices.Contains(in, s) {
			out = append(out, s)
		}
	}
	return out, true
}

type clientBody struct {
	Name    string   `json:"name"`
	Scopes  []string `json:"scopes"`
	Enabled *bool    `json:"enabled"`
}

func (b *clientBody) check() (string, []string, string) {
	name := strings.TrimSpace(b.Name)
	if name == "" || len(name) > 80 {
		return "", nil, "name required (at most 80 characters)"
	}
	scopes, ok := cleanScopes(b.Scopes)
	if !ok {
		return "", nil, "scopes must be some of " + strings.Join(ext.Scopes, ", ")
	}
	return name, scopes, ""
}

func (s *Server) listClients(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListAPIClients(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": list, "scopes": ext.Scopes})
}

// createClient returns the secret once; only its hash is stored.
func (s *Server) createClient(w http.ResponseWriter, r *http.Request) {
	var body clientBody
	if decode(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	name, scopes, problem := body.check()
	if problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}
	secret := newClientSecret()
	c := store.APIClient{ClientID: newClientID(), Name: name, Scopes: scopes, Enabled: true,
		CreatedBy: userOf(r).Username, SecretHash: auth.HashToken(secret)}
	id, err := s.store.CreateAPIClient(r.Context(), c)
	if err != nil {
		fail(w, err)
		return
	}
	created, err := s.store.GetAPIClient(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	s.audit(r, "api_client.create", created.ClientID, map[string]any{"name": name, "scopes": scopes})
	writeJSON(w, http.StatusCreated, map[string]any{"client": created, "client_secret": secret})
}

func (s *Server) updateClient(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	current, err := s.store.GetAPIClient(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	var body clientBody
	if decode(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	name, scopes, problem := body.check()
	if problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}
	enabled := current.Enabled
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	if err := s.store.UpdateAPIClient(r.Context(), id, name, scopes, enabled); err != nil {
		fail(w, err)
		return
	}
	s.audit(r, "api_client.update", current.ClientID, map[string]any{"name": name, "scopes": scopes, "enabled": enabled})
	updated, err := s.store.GetAPIClient(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// rotateClientSecret issues a new secret; the old one and its tokens stop working at once.
func (s *Server) rotateClientSecret(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	current, err := s.store.GetAPIClient(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	secret := newClientSecret()
	if err := s.store.SetAPIClientSecret(r.Context(), id, auth.HashToken(secret)); err != nil {
		fail(w, err)
		return
	}
	s.audit(r, "api_client.rotate_secret", current.ClientID, nil)
	writeJSON(w, http.StatusOK, map[string]string{"client_id": current.ClientID, "client_secret": secret})
}

func (s *Server) deleteClient(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	current, err := s.store.GetAPIClient(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.store.DeleteAPIClient(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	s.audit(r, "api_client.delete", current.ClientID, map[string]any{"name": current.Name})
	w.WriteHeader(http.StatusNoContent)
}

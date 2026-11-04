package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/auth"
	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
	"github.com/DarmawanWeb/netra-fleet-be/internal/version"
)

// The first-run setup: a fresh installation has no user; the dashboard opens the setup wizard,
// which shows the installation ID (licenses are bound to it), creates the first administrator and
// installs the license. Once a user exists the setup is closed for good.

func (s *Server) setupOpen(ctx context.Context) (bool, error) {
	has, err := s.store.HasUsers(ctx)
	return !has, err
}

// setupState is public: the login page asks it whether to open the wizard.
func (s *Server) setupState(w http.ResponseWriter, r *http.Request) {
	open, err := s.setupOpen(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"required":        open,
		"installation_id": s.lic.Installation(),
		"version":         version.String(),
		"license":         s.lic.Info(),
	})
}

// setup creates the first administrator, installs the license when one is given, and answers
// like a login so the wizard continues signed in.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
		License     string `json:"license"`
	}
	if decode(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "username and password required")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	body.License = strings.TrimSpace(body.License)
	if !usernamePattern.MatchString(body.Username) {
		writeError(w, http.StatusBadRequest, "username: 3 to 32 characters of a-z, 0-9, . _ -")
		return
	}
	if !validPassword(body.Password) {
		writeError(w, http.StatusBadRequest, "password: at least 8 characters")
		return
	}
	if body.License != "" {
		if _, err := s.lic.Check(body.License); err != nil {
			writeError(w, http.StatusBadRequest, "license: "+err.Error())
			return
		}
	}
	ctx := r.Context()
	id, err := s.store.CreateFirstAdmin(ctx, store.User{Username: body.Username, DisplayName: body.DisplayName},
		auth.HashPassword(body.Password))
	if errors.Is(err, store.ErrSetupDone) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		fail(w, err)
		return
	}
	ip := clientIP(r)
	s.auditAs(body.Username, "setup", ip)
	if body.License != "" {
		if _, err := s.lic.Apply(body.License, "upload"); err == nil {
			_ = s.store.SaveLicense(ctx, body.License, body.Username)
			s.hub.Broadcast(map[string]any{"type": "license", "license": s.lic.Info()})
		}
	}
	token := auth.NewToken()
	expires := time.Now().Add(s.cfg.SessionTTL)
	if err := s.store.CreateSessionFor(ctx, auth.HashToken(token), id, expires, ip); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "expires_at": expires, "username": body.Username,
		"role": "admin", "permissions": auth.PermissionsOf("admin"), "license": s.lic.Info()})
}

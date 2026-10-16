package ext

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/auth"
	"github.com/DarmawanWeb/netra-fleet-be/internal/store"
)

// TokenPrefix marks NETRA integration access tokens (easy to spot in logs and secret scanners).
const TokenPrefix = "nat_"

type tokenRequest struct {
	GrantType    string `json:"grant_type"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	Scope        string `json:"scope"`
}

// readTokenRequest accepts the client credentials in HTTP Basic (preferred) or in the body,
// form-encoded (RFC 6749) or JSON.
func readTokenRequest(r *http.Request) (tokenRequest, error) {
	var req tokenRequest
	r.Body = http.MaxBytesReader(nil, r.Body, 64<<10)
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	switch ct {
	case "application/json":
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			return req, errors.New("the body is not valid JSON")
		}
	default:
		if err := r.ParseForm(); err != nil {
			return req, errors.New("the body is not a valid form")
		}
		req = tokenRequest{GrantType: r.PostForm.Get("grant_type"), ClientID: r.PostForm.Get("client_id"),
			ClientSecret: r.PostForm.Get("client_secret"), Scope: r.PostForm.Get("scope")}
	}
	if id, secret, ok := r.BasicAuth(); ok {
		// RFC 6749 2.3.1: both are form-encoded before they go into the Basic header.
		if v, err := url.QueryUnescape(id); err == nil {
			id = v
		}
		if v, err := url.QueryUnescape(secret); err == nil {
			secret = v
		}
		req.ClientID, req.ClientSecret = id, secret
	}
	return req, nil
}

// token issues an access token for the client credentials grant.
func (a *API) token(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	ip := clientIP(r)
	if a.failures.blocked(ip) {
		w.Header().Set("Retry-After", retryAfter(failureWindow))
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many failed attempts, try again in 10 minutes")
		return
	}
	req, err := readTokenRequest(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if req.GrantType != "client_credentials" {
		writeErr(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be client_credentials")
		return
	}
	if req.ClientID == "" || req.ClientSecret == "" {
		writeErr(w, http.StatusBadRequest, "invalid_request", "client_id and client_secret are required")
		return
	}
	client, err := a.store.APIClientByClientID(r.Context(), req.ClientID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		fail(w, err)
		return
	}
	// The secret is 256 random bits, so its SHA-256 is a safe verifier; compare in constant time.
	ok := err == nil && client.Enabled &&
		subtle.ConstantTimeCompare([]byte(auth.HashToken(req.ClientSecret)), []byte(client.SecretHash)) == 1
	if !ok {
		a.failures.add(ip)
		w.Header().Set("WWW-Authenticate", `Basic realm="netra"`)
		writeErr(w, http.StatusUnauthorized, "invalid_client", "unknown client, wrong secret or client disabled")
		return
	}
	a.failures.reset(ip)

	scopes := client.Scopes
	if s := strings.Fields(req.Scope); len(s) > 0 {
		for _, want := range s {
			if !slices.Contains(client.Scopes, want) {
				writeErr(w, http.StatusBadRequest, "invalid_scope", "this client may not have the scope "+want)
				return
			}
		}
		slices.Sort(s)
		scopes = slices.Compact(s)
	}
	if len(scopes) == 0 {
		writeErr(w, http.StatusBadRequest, "invalid_scope", "this client has no scopes; ask the NETRA administrator")
		return
	}

	token := TokenPrefix + auth.NewToken()
	ttl := a.cfg.ExtTokenTTL
	if ttl <= 0 {
		ttl = time.Hour
	}
	if err := a.store.CreateAPIToken(r.Context(), auth.HashToken(token), client.ID, scopes, time.Now().Add(ttl)); err != nil {
		slog.Error("integration token", "err", err)
		writeErr(w, http.StatusInternalServerError, "server_error", "could not issue a token, try again later")
		return
	}
	a.audit(r, client, "ext.token", map[string]any{"scopes": scopes})
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": token,
		"token_type":   "Bearer",
		"expires_in":   int(ttl.Seconds()),
		"scope":        strings.Join(scopes, " "),
	})
}

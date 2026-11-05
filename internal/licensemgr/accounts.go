package licensemgr

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/DarmawanWeb/netra-fleet-be/internal/auth"
	"github.com/DarmawanWeb/netra-fleet-be/internal/version"
)

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (c *credentials) check() error {
	c.Username = strings.TrimSpace(c.Username)
	if !usernamePattern.MatchString(c.Username) {
		return fieldErr("username", "username: 3 to 32 characters of a-z, 0-9, . _ -")
	}
	if len(c.Password) < minPassword {
		return fieldErr("password", "password: at least %d characters", minPassword)
	}
	return nil
}

type operatorView struct {
	Username  string     `json:"username"`
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy string     `json:"created_by,omitempty"`
	LastLogin *time.Time `json:"last_login,omitempty"`
}

func viewOperator(u User) operatorView {
	return operatorView{Username: u.Username, CreatedAt: u.CreatedAt, CreatedBy: u.CreatedBy, LastLogin: u.LastLogin}
}

// sessionState is public: the UI asks it whether to show the first-run setup, the login or the app.
func (s *Server) sessionState(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"setup": !s.store.HasUsers(), "version": version.String(), "user": nil}
	if name, ok := s.sessions.user(sessionToken(r), s.now()); ok {
		if u, ok := s.store.User(name); ok {
			out["user"] = viewOperator(u)
		}
	}
	reply(w, http.StatusOK, out)
}

// setup creates the first operator and signs in; once one exists it is closed for good.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var body credentials
	if err := decode(w, r, &body); err != nil {
		replyErr(w, err)
		return
	}
	if err := body.check(); err != nil {
		replyErr(w, err)
		return
	}
	now := s.now().UTC()
	u := User{Username: body.Username, Password: auth.HashPassword(body.Password), CreatedAt: now, LastLogin: &now}
	err := s.store.AddUser(u, true)
	if errors.Is(err, ErrExists) {
		replyError(w, http.StatusConflict, "the first operator exists already: log in")
		return
	}
	if err != nil {
		replyErr(w, err)
		return
	}
	s.record(r, u.Username, "setup", u.Username, nil)
	s.startSession(w, r, u.Username)
	reply(w, http.StatusCreated, map[string]any{"user": viewOperator(u)})
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// noUser is a hash to check a password against when the user does not exist, so both cases take
// as long.
func noUser() string {
	dummyOnce.Do(func() { dummyHash = auth.HashPassword(auth.NewToken()) })
	return dummyHash
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	key := "login " + clientIP(r)
	if s.limiter.blocked(key, s.now()) {
		replyError(w, http.StatusTooManyRequests, "too many failed logins: try again in 10 minutes")
		return
	}
	var body credentials
	if err := decode(w, r, &body); err != nil {
		replyErr(w, err)
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	u, ok := s.store.User(body.Username)
	hash := u.Password
	if !ok {
		hash = noUser()
	}
	if !auth.CheckPassword(body.Password, hash) || !ok {
		s.limiter.fail(key, s.now())
		s.record(r, cleanText(body.Username, 64), "login_failed", "", nil)
		time.Sleep(s.failDelay)
		replyError(w, http.StatusUnauthorized, "wrong username or password")
		return
	}
	s.limiter.reset(key)
	now := s.now().UTC()
	_ = s.store.UpdateUser(u.Username, func(x *User) { x.LastLogin = &now })
	s.record(r, u.Username, "login", "", nil)
	s.startSession(w, r, u.Username)
	reply(w, http.StatusOK, map[string]any{"user": viewOperator(u)})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, user string) {
	s.sessions.drop(sessionToken(r))
	setCookie(w, r, "", -1)
	s.record(r, user, "logout", "", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request, user string) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decode(w, r, &body); err != nil {
		replyErr(w, err)
		return
	}
	u, _ := s.store.User(user)
	if !auth.CheckPassword(body.Current, u.Password) {
		replyErr(w, fieldErr("current", "the current password is wrong"))
		return
	}
	if len(body.New) < minPassword {
		replyErr(w, fieldErr("new", "password: at least %d characters", minPassword))
		return
	}
	hash := auth.HashPassword(body.New)
	if err := s.store.UpdateUser(user, func(x *User) { x.Password = hash }); err != nil {
		replyErr(w, err)
		return
	}
	s.sessions.dropUser(user, sessionToken(r))
	s.record(r, user, "password.change", user, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listOperators(w http.ResponseWriter, _ *http.Request, _ string) {
	users := s.store.Users()
	out := make([]operatorView, 0, len(users))
	for _, u := range users {
		out = append(out, viewOperator(u))
	}
	reply(w, http.StatusOK, out)
}

func (s *Server) addOperator(w http.ResponseWriter, r *http.Request, user string) {
	var body credentials
	if err := decode(w, r, &body); err != nil {
		replyErr(w, err)
		return
	}
	if err := body.check(); err != nil {
		replyErr(w, err)
		return
	}
	u := User{Username: body.Username, Password: auth.HashPassword(body.Password), CreatedAt: s.now().UTC(), CreatedBy: user}
	err := s.store.AddUser(u, false)
	if errors.Is(err, ErrExists) {
		replyErr(w, fieldErr("username", "%s exists already", u.Username))
		return
	}
	if err != nil {
		replyErr(w, err)
		return
	}
	s.record(r, user, "operator.create", u.Username, nil)
	reply(w, http.StatusCreated, viewOperator(u))
}

func (s *Server) removeOperator(w http.ResponseWriter, r *http.Request, user string) {
	name := r.PathValue("name")
	if name == user {
		replyError(w, http.StatusConflict, "you cannot remove yourself")
		return
	}
	err := s.store.DeleteUser(name)
	if errors.Is(err, ErrNotFound) {
		replyErr(w, err)
		return
	}
	if err != nil {
		replyError(w, http.StatusConflict, err.Error())
		return
	}
	s.sessions.dropUser(name, "")
	s.record(r, user, "operator.delete", name, nil)
	w.WriteHeader(http.StatusNoContent)
}

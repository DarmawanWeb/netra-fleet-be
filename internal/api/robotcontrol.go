package api

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"time"
)

// ackTimeout is how long a control request waits for the robot's ack.
var ackTimeout = 10 * time.Second

type ackResult struct {
	OK  bool   `json:"ok"`
	Msg string `json:"msg"`
}

var errAckTimeout = errors.New("the robot did not answer within 10 s")

// commandAndWait sends a command and blocks until the robot acks it or the timeout passes.
func (s *Server) commandAndWait(robotID, name string, args map[string]any) (ackResult, error) {
	done := make(chan ackResult, 1)
	id := fmt.Sprintf("%s-%d", name, time.Now().UnixNano())
	s.mu.Lock()
	s.pending[id] = pendingCommand{name: name, done: done}
	s.mu.Unlock()
	if err := s.hub.SendRobot(robotID, map[string]any{"type": "command", "id": id, "name": name, "args": args}); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return ackResult{}, err
	}
	select {
	case res := <-done:
		return res, nil
	case <-time.After(ackTimeout):
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return ackResult{}, errAckTimeout
	}
}

// inspectionArgs checks a set_inspection request; at least one setting must be given.
func (s *Server) inspectionArgs(enabled *bool, model *string, interval *float64) (map[string]any, error) {
	args := map[string]any{}
	if enabled != nil {
		args["enabled"] = *enabled
	}
	if model != nil {
		if !slices.Contains(s.cfg.InspectionModels, *model) {
			return nil, fmt.Errorf("unknown model %q", *model)
		}
		args["model"] = *model
	}
	if interval != nil {
		v := *interval
		if math.IsNaN(v) || math.IsInf(v, 0) || v > 3600 {
			return nil, errors.New("interval must be 0 (continuous), 1-3600 s, or negative (stop points only)")
		}
		args["interval"] = v
	}
	if len(args) == 0 {
		return nil, errors.New("give enabled, model or interval")
	}
	return args, nil
}

func (s *Server) replyAck(w http.ResponseWriter, r *http.Request, action string, args map[string]any, res ackResult, err error) {
	robotID := r.PathValue("id")
	detail := map[string]any{"args": args}
	switch {
	case errors.Is(err, errAckTimeout):
		detail["error"] = err.Error()
		s.audit(r, action, robotID, detail)
		writeError(w, http.StatusGatewayTimeout, err.Error())
	case err != nil:
		detail["error"] = err.Error()
		s.audit(r, action, robotID, detail)
		writeError(w, http.StatusConflict, err.Error())
	default:
		detail["ok"], detail["msg"] = res.OK, res.Msg
		s.audit(r, action, robotID, detail)
		if !res.OK {
			writeJSON(w, http.StatusUnprocessableEntity, res)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// robotInspection turns on-robot AI inspection on or off, or changes its model or rhythm.
func (s *Server) robotInspection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled  *bool    `json:"enabled"`
		Model    *string  `json:"model"`
		Interval *float64 `json:"interval"`
	}
	if decode(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	args, err := s.inspectionArgs(body.Enabled, body.Model, body.Interval)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := s.commandAndWait(r.PathValue("id"), "set_inspection", args)
	s.replyAck(w, r, "robot.inspection", args, res, err)
}

// robotPeople turns on-robot people detection on or off.
func (s *Server) robotPeople(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if decode(r, &body) != nil || body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "enabled (true or false) is required")
		return
	}
	args := map[string]any{"enabled": *body.Enabled}
	res, err := s.commandAndWait(r.PathValue("id"), "set_people", args)
	s.replyAck(w, r, "robot.people", args, res, err)
}

func (s *Server) inspectionModels(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg.InspectionModels)
}

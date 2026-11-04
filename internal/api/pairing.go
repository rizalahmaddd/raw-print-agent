package api

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rizalahmaddd/raw-print-agent/internal/config"
)

const (
	pairTTL     = 10 * time.Minute
	pairMaxOpen = 10
)

type pairStatus string

const (
	pairPending  pairStatus = "pending"
	pairApproved pairStatus = "approved"
	pairRejected pairStatus = "rejected"
)

type pairing struct {
	ID        string     `json:"id"`
	Code      string     `json:"code"`
	Origin    string     `json:"origin"`
	Name      string     `json:"name"`
	Status    pairStatus `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	secret    string
	tokenID   string
}

type pairings struct {
	mu sync.Mutex
	m  map[string]*pairing
}

func newPairings() *pairings { return &pairings{m: map[string]*pairing{}} }

func (p *pairings) prune(now time.Time) {
	for id, x := range p.m {
		if now.Sub(x.CreatedAt) > pairTTL {
			delete(p.m, id)
		}
	}
}

func sixDigits() string {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%06d", n.Int64())
}

type pairReq struct {
	Name string `json:"name"`
}

func (s *Server) handlePairStart(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	if rc.kind != kindRemote {
		writeErr(w, http.StatusBadRequest, "origin_required", "pairing must be started from an allowlisted web page")
		return
	}
	var req pairReq
	if r.ContentLength != 0 && !readJSON(w, r, &req, 4<<10) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if len(name) > 80 {
		name = name[:80]
	}
	if name == "" {
		name = rc.origin
	}

	if s.store.Snapshot().AutoApprovePairing {
		var secret string
		var tok config.Token
		if err := s.store.Update(func(c *config.Config) error {
			tok, secret = c.IssueToken(name, rc.origin)
			return nil
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, "config_error", err.Error())
			return
		}
		s.log.Info("pairing auto-approved", "origin", rc.origin, "name", name, "token_id", tok.ID)
		writeJSON(w, http.StatusOK, map[string]any{"status": pairApproved, "token": secret, "token_id": tok.ID})
		return
	}

	now := s.now()
	s.pairs.mu.Lock()
	s.pairs.prune(now)
	open := 0
	for _, x := range s.pairs.m {
		if x.Status == pairPending {
			open++
		}
	}
	if open >= pairMaxOpen {
		s.pairs.mu.Unlock()
		writeErr(w, http.StatusTooManyRequests, "too_many_pairings", "too many pending pairing requests, approve or wait")
		return
	}
	pr := &pairing{ID: config.RandomID(), Code: sixDigits(), Origin: rc.origin, Name: name, Status: pairPending, CreatedAt: now}
	s.pairs.m[pr.ID] = pr
	s.pairs.mu.Unlock()

	s.log.Info("pairing requested", "origin", rc.origin, "name", name, "id", pr.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status": pr.Status, "request_id": pr.ID, "code": pr.Code,
		"approve_url": fmt.Sprintf("http://127.0.0.1:%s/#pair", s.port),
		"expires_in":  int(pairTTL.Seconds()),
	})
}

func (s *Server) handlePairPoll(w http.ResponseWriter, r *http.Request, rc *reqCtx) {
	s.pairs.mu.Lock()
	defer s.pairs.mu.Unlock()
	s.pairs.prune(s.now())
	pr, ok := s.pairs.m[r.PathValue("id")]
	if !ok || !strings.EqualFold(pr.Origin, rc.origin) {
		writeErr(w, http.StatusNotFound, "pairing_not_found", "unknown or expired pairing request")
		return
	}
	switch pr.Status {
	case pairApproved:
		delete(s.pairs.m, pr.ID)
		writeJSON(w, http.StatusOK, map[string]any{"status": pr.Status, "token": pr.secret, "token_id": pr.tokenID})
	case pairRejected:
		delete(s.pairs.m, pr.ID)
		writeJSON(w, http.StatusOK, map[string]any{"status": pr.Status})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"status": pr.Status, "code": pr.Code})
	}
}

func (s *Server) pendingPairs() []pairing {
	s.pairs.mu.Lock()
	defer s.pairs.mu.Unlock()
	s.pairs.prune(s.now())
	out := []pairing{}
	for _, x := range s.pairs.m {
		if x.Status == pairPending {
			out = append(out, *x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *Server) handleAdminApprove(w http.ResponseWriter, r *http.Request) {
	s.pairs.mu.Lock()
	defer s.pairs.mu.Unlock()
	pr, ok := s.pairs.m[r.PathValue("id")]
	if !ok || pr.Status != pairPending {
		writeErr(w, http.StatusNotFound, "pairing_not_found", "unknown or expired pairing request")
		return
	}
	var tok config.Token
	var secret string
	if err := s.store.Update(func(c *config.Config) error {
		tok, secret = c.IssueToken(pr.Name, pr.Origin)
		return nil
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, "config_error", err.Error())
		return
	}
	pr.Status, pr.secret, pr.tokenID = pairApproved, secret, tok.ID
	s.log.Info("pairing approved", "origin", pr.Origin, "name", pr.Name, "token_id", tok.ID)
	writeJSON(w, http.StatusOK, map[string]any{"status": pr.Status, "token_id": tok.ID})
}

func (s *Server) handleAdminReject(w http.ResponseWriter, r *http.Request) {
	s.pairs.mu.Lock()
	defer s.pairs.mu.Unlock()
	pr, ok := s.pairs.m[r.PathValue("id")]
	if !ok || pr.Status != pairPending {
		writeErr(w, http.StatusNotFound, "pairing_not_found", "unknown or expired pairing request")
		return
	}
	pr.Status = pairRejected
	s.log.Info("pairing rejected", "origin", pr.Origin, "name", pr.Name)
	writeJSON(w, http.StatusOK, map[string]any{"status": pr.Status})
}

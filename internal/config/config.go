package config

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const DefaultListen = "127.0.0.1:18310"

type Profile struct {
	PageHeightMM   float64 `json:"page_height_mm"`
	TopMarginLines int     `json:"top_margin_lines"`
	LeftMarginCols int     `json:"left_margin_cols"`
	Quality        string  `json:"quality"`
	CPI            int     `json:"cpi"`
}

func DefaultProfile() Profile {
	return Profile{PageHeightMM: 279.4, TopMarginLines: 3, LeftMarginCols: 0, Quality: "draft", CPI: 12}
}

func (p Profile) Validate() error {
	if p.PageHeightMM < 50 || p.PageHeightMM > 600 {
		return errors.New("page_height_mm must be between 50 and 600")
	}
	if p.TopMarginLines < 0 || p.TopMarginLines > 20 {
		return errors.New("top_margin_lines must be between 0 and 20")
	}
	if p.LeftMarginCols < 0 || p.LeftMarginCols > 40 {
		return errors.New("left_margin_cols must be between 0 and 40")
	}
	if p.Quality != "draft" && p.Quality != "nlq" {
		return errors.New("quality must be draft or nlq")
	}
	if p.CPI != 10 && p.CPI != 12 && p.CPI != 17 {
		return errors.New("cpi must be 10, 12 or 17")
	}
	return nil
}

type Token struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Origin     string    `json:"origin"`
	Hash       string    `json:"hash"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

type Config struct {
	Listen              string             `json:"listen"`
	AllowedOrigins      []string           `json:"allowed_origins"`
	AutoApprovePairing  bool               `json:"auto_approve_pairing"`
	DefaultPrinter      string             `json:"default_printer"`
	DefaultPrinterMatch string             `json:"default_printer_match"`
	MaxPayloadBytes     int64              `json:"max_payload_bytes"`
	LogRetentionDays    int                `json:"log_retention_days"`
	Profiles            map[string]Profile `json:"profiles"`
	Tokens              []Token            `json:"tokens"`
}

func Default() Config {
	return Config{
		Listen:           DefaultListen,
		AllowedOrigins:   []string{},
		MaxPayloadBytes:  2 << 20,
		LogRetentionDays: 7,
		Profiles:         map[string]Profile{},
		Tokens:           []Token{},
	}
}

// Store guards the config file. Every mutation is persisted before it returns,
// so a crash never leaves a paired token only in memory.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, cfg: Default()}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		return s, s.saveLocked()
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(b, &s.cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	s.fillDefaults()
	if err := ValidateListen(s.cfg.Listen); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Path() string { return s.path }

func (s *Store) fillDefaults() {
	d := Default()
	if s.cfg.Listen == "" {
		s.cfg.Listen = d.Listen
	}
	if s.cfg.MaxPayloadBytes <= 0 {
		s.cfg.MaxPayloadBytes = d.MaxPayloadBytes
	}
	if s.cfg.LogRetentionDays <= 0 {
		s.cfg.LogRetentionDays = d.LogRetentionDays
	}
	if s.cfg.Profiles == nil {
		s.cfg.Profiles = map[string]Profile{}
	}
	if s.cfg.AllowedOrigins == nil {
		s.cfg.AllowedOrigins = []string{}
	}
	if s.cfg.Tokens == nil {
		s.cfg.Tokens = []Token{}
	}
}

// Snapshot returns a deep copy that callers may read freely.
func (s *Store) Snapshot() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c := s.cfg
	c.AllowedOrigins = append([]string(nil), s.cfg.AllowedOrigins...)
	c.Tokens = append([]Token(nil), s.cfg.Tokens...)
	c.Profiles = make(map[string]Profile, len(s.cfg.Profiles))
	for k, v := range s.cfg.Profiles {
		c.Profiles[k] = v
	}
	return c
}

func (s *Store) Update(fn func(c *Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.cfg
	prev.AllowedOrigins = append([]string(nil), s.cfg.AllowedOrigins...)
	prev.Tokens = append([]Token(nil), s.cfg.Tokens...)
	prev.Profiles = make(map[string]Profile, len(s.cfg.Profiles))
	for k, v := range s.cfg.Profiles {
		prev.Profiles[k] = v
	}
	if err := fn(&s.cfg); err != nil {
		s.cfg = prev
		return err
	}
	s.fillDefaults()
	if err := s.saveLocked(); err != nil {
		s.cfg = prev
		return err
	}
	return nil
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (c Config) OriginAllowed(origin string) bool {
	for _, o := range c.AllowedOrigins {
		if strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}

func (c Config) ProfileFor(printer string) Profile {
	if p, ok := c.Profiles[printer]; ok {
		return p
	}
	if p, ok := c.Profiles["*"]; ok {
		return p
	}
	return DefaultProfile()
}

// IssueToken stores only the SHA-256 of the secret; the plaintext is returned once.
func (c *Config) IssueToken(name, origin string) (Token, string) {
	secret := randomHex(32)
	t := Token{
		ID:        randomHex(8),
		Name:      name,
		Origin:    origin,
		Hash:      hashSecret(secret),
		CreatedAt: time.Now().UTC(),
	}
	c.Tokens = append(c.Tokens, t)
	return t, secret
}

func (c Config) FindToken(secret string) (int, bool) {
	if secret == "" {
		return -1, false
	}
	h := hashSecret(secret)
	for i, t := range c.Tokens {
		if subtle.ConstantTimeCompare([]byte(t.Hash), []byte(h)) == 1 {
			return i, true
		}
	}
	return -1, false
}

func (c *Config) RevokeToken(id string) bool {
	for i, t := range c.Tokens {
		if t.ID == id {
			c.Tokens = append(c.Tokens[:i], c.Tokens[i+1:]...)
			return true
		}
	}
	return false
}

func NormalizeOrigin(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("invalid origin %q (expected scheme://host[:port])", raw)
	}
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("origin %q must not contain a path", raw)
	}
	return strings.ToLower(u.Scheme + "://" + u.Host), nil
}

func ParseOrigins(list string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(list, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		o, err := NormalizeOrigin(part)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

// ValidateListen refuses anything but loopback: exposing the agent on the LAN
// would let any machine on the network print.
func ValidateListen(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", addr, err)
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address %q must be a loopback address", addr)
	}
	return nil
}

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func RandomID() string { return randomHex(8) }

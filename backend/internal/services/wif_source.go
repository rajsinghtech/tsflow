package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"golang.org/x/oauth2"
)

// wifRefreshBefore is how early a cached Tailscale credential is replaced.
// A projected service account token is rotated on disk before this window,
// so the next read sees the new JWT instead of exchanging an expired one.
const wifRefreshBefore = time.Minute

// wifSource exchanges one tailnet's OIDC token for a Tailscale API token.
// The API token and the ID token are cached on this value, so each tailnet
// service has its own cache and a poll does not exchange once per request.
type wifSource struct {
	clientID      string
	baseURL       string
	httpClient    *http.Client
	loadIDToken   func() (string, error)
	now           func() time.Time
	refreshBefore time.Duration

	mu      sync.Mutex
	idToken string
	idExp   time.Time
	idLife  time.Duration
	api     *oauth2.Token
}

func newWIFSource(baseURL string, cfg *config.Config) *wifSource {
	return &wifSource{
		clientID:      cfg.TailscaleWIFClientID,
		baseURL:       strings.TrimRight(baseURL, "/"),
		httpClient:    &http.Client{Timeout: 30 * time.Second},
		loadIDToken:   tailscaleIDTokenFunc(cfg),
		now:           time.Now,
		refreshBefore: wifRefreshBefore,
	}
}

func (s *wifSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if err := s.ensureIDToken(now); err != nil {
		return nil, err
	}
	if s.api != nil && !s.expiring(s.api.Expiry, s.apiLife(), now) {
		return s.api, nil
	}
	exchanged, err := s.exchange(s.idToken)
	if err != nil {
		return nil, err
	}
	s.api = exchanged
	return s.api, nil
}

func (s *wifSource) apiLife() time.Duration {
	if s.api == nil || s.api.Expiry.IsZero() {
		return 0
	}
	// Expiry is absolute. The lifetime used to size the lead was stored by
	// setting Expiry from now+expires_in at exchange time. Recompute against
	// the clock captured then via the token's extra field.
	if life, ok := s.api.Extra("lifetime").(time.Duration); ok {
		return life
	}
	return 0
}

func (s *wifSource) ensureIDToken(now time.Time) error {
	if s.idToken != "" && !s.expiring(s.idExp, s.idLife, now) {
		return nil
	}
	raw, err := s.loadIDToken()
	if err != nil {
		return err
	}
	exp, err := jwtExpiry(raw)
	if err != nil {
		return err
	}
	if !exp.After(now) {
		return fmt.Errorf("tailscale workload identity token is expired")
	}
	s.idToken = raw
	s.idExp = exp
	s.idLife = exp.Sub(now)
	return nil
}

func (s *wifSource) expiring(exp time.Time, life time.Duration, now time.Time) bool {
	lead := s.refreshBefore
	if lead <= 0 {
		lead = wifRefreshBefore
	}
	// A short-lived token still refreshes before it expires, but not on every
	// call. One fifth of its original life is the early window, floored at one
	// second.
	if life > 0 && lead >= life/2 {
		lead = life / 5
		if lead < time.Second {
			lead = time.Second
		}
	}
	return !exp.After(now.Add(lead))
}

func (s *wifSource) exchange(idToken string) (*oauth2.Token, error) {
	body := url.Values{
		"client_id": {s.clientID},
		"jwt":       {idToken},
	}.Encode()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, s.baseURL+"/api/v2/oauth/token-exchange", strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("tailscale token exchange: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tailscale token exchange: %w", err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("tailscale token exchange: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("tailscale token exchange failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}
	var exchanged struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(payload, &exchanged); err != nil {
		return nil, fmt.Errorf("tailscale token exchange: %w", err)
	}
	if exchanged.AccessToken == "" {
		return nil, fmt.Errorf("tailscale token exchange returned an empty access token")
	}
	life := time.Duration(exchanged.ExpiresIn) * time.Second
	if life <= 0 {
		life = time.Hour
	}
	token := &oauth2.Token{
		AccessToken: exchanged.AccessToken,
		TokenType:   exchanged.TokenType,
		Expiry:      s.now().Add(life),
	}
	return token.WithExtra(map[string]any{"lifetime": life}), nil
}

func jwtExpiry(token string) (time.Time, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, fmt.Errorf("tailscale workload identity token is not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, fmt.Errorf("tailscale workload identity token payload is not valid base64")
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, fmt.Errorf("tailscale workload identity token claims are not valid JSON")
	}
	if claims.Exp == 0 {
		return time.Time{}, fmt.Errorf("tailscale workload identity token is missing exp")
	}
	return time.Unix(claims.Exp, 0), nil
}

func wifHTTPClient(baseURL string, cfg *config.Config) *http.Client {
	source := newWIFSource(baseURL, cfg)
	return &http.Client{
		Timeout: 30 * time.Minute,
		Transport: &oauth2.Transport{
			Source: source,
		},
	}
}

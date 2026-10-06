package tsnetserve

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"tailscale.com/client/local"
	"tailscale.com/feature"
	"tailscale.com/tsnet"

	// Register the tsnet workload identity hook. Without this import,
	// TS_CLIENT_ID is copied onto the server and then ignored, and the node
	// falls through to an interactive login instead of joining with WIF.
	_ "tailscale.com/feature/identityfederation"
)

type Server struct {
	tsServer     *tsnet.Server
	tlsListener  net.Listener
	httpListener net.Listener
}

func New(ctx context.Context, cfg *config.Config) (*Server, error) {
	if err := os.MkdirAll(cfg.TsnetStateDir, 0700); err != nil {
		return nil, fmt.Errorf("creating tsnet state dir: %w", err)
	}

	srv := &tsnet.Server{
		Dir:           cfg.TsnetStateDir,
		Hostname:      cfg.TsnetHostname,
		Ephemeral:     true,
		AdvertiseTags: cfg.TsnetTags,
	}

	if cfg.TsnetClientID != "" {
		// The feature init sets the auth-key hook only when registration
		// succeeds. TS_DISABLE_FEATURE=identityfederation leaves the fields
		// set and the hook unset, which would silently skip WIF.
		if !feature.IsRegistered("identityfederation") {
			return nil, fmt.Errorf("tsnet workload identity is not linked; TS_CLIENT_ID cannot join a tailnet without an auth key")
		}
		srv.ClientID = cfg.TsnetClientID
		srv.IDToken = cfg.TsnetIDToken
		srv.Audience = cfg.TsnetAudience
	} else {
		srv.ClientSecret = cfg.TailscaleOAuthClientSecret
	}

	if _, err := srv.Up(ctx); err != nil {
		return nil, fmt.Errorf("tsnet up: %w", err)
	}

	var tlsLn, httpLn net.Listener
	var err error

	if cfg.TsnetFunnel {
		tlsLn, err = srv.ListenFunnel("tcp", ":443")
	} else {
		tlsLn, err = srv.ListenTLS("tcp", ":443")
	}
	if err != nil {
		srv.Close()
		return nil, fmt.Errorf("tsnet listen TLS: %w", err)
	}

	httpLn, err = srv.Listen("tcp", ":80")
	if err != nil {
		tlsLn.Close()
		srv.Close()
		return nil, fmt.Errorf("tsnet listen HTTP: %w", err)
	}

	mode := "tailnet"
	if cfg.TsnetFunnel {
		mode = "funnel"
	}
	if domains := srv.CertDomains(); len(domains) > 0 {
		log.Printf("tsnet: serving via %s at https://%s (443) and http://%s (80)", mode, domains[0], domains[0])
	}

	return &Server{tsServer: srv, tlsListener: tlsLn, httpListener: httpLn}, nil
}

func (s *Server) TLSListener() net.Listener  { return s.tlsListener }
func (s *Server) HTTPListener() net.Listener { return s.httpListener }

// LocalClient is the tsnet node's LocalAPI client. WhoIs on a request
// RemoteAddr reads the peer and its application capability grants.
func (s *Server) LocalClient() (*local.Client, error) {
	if s == nil || s.tsServer == nil {
		return nil, fmt.Errorf("tsnet server is not running")
	}
	return s.tsServer.LocalClient()
}

func (s *Server) Close() error {
	if s.httpListener != nil {
		s.httpListener.Close()
	}
	if s.tlsListener != nil {
		s.tlsListener.Close()
	}
	return s.tsServer.Close()
}

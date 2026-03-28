package tsnetserve

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/rajsinghtech/tsflow/backend/internal/config"
	"tailscale.com/tsnet"
)

type Server struct {
	tsServer *tsnet.Server
	listener net.Listener
}

func New(ctx context.Context, cfg *config.Config) (*Server, error) {
	if err := os.MkdirAll(cfg.TsnetStateDir, 0700); err != nil {
		return nil, fmt.Errorf("creating tsnet state dir: %w", err)
	}

	srv := &tsnet.Server{
		Dir:           cfg.TsnetStateDir,
		Hostname:      cfg.TsnetHostname,
		ClientSecret:  cfg.TailscaleOAuthClientSecret,
		Ephemeral:     true,
		AdvertiseTags: cfg.TsnetTags,
	}

	if _, err := srv.Up(ctx); err != nil {
		return nil, fmt.Errorf("tsnet up: %w", err)
	}

	var ln net.Listener
	var err error
	if cfg.TsnetFunnel {
		ln, err = srv.ListenFunnel("tcp", ":443")
	} else {
		ln, err = srv.ListenTLS("tcp", ":443")
	}
	if err != nil {
		srv.Close()
		return nil, fmt.Errorf("tsnet listen: %w", err)
	}

	mode := "tailnet"
	if cfg.TsnetFunnel {
		mode = "funnel"
	}
	if domains := srv.CertDomains(); len(domains) > 0 {
		log.Printf("tsnet: serving via %s at https://%s", mode, domains[0])
	}

	return &Server{tsServer: srv, listener: ln}, nil
}

func (s *Server) Listener() net.Listener { return s.listener }

func (s *Server) Close() error {
	if s.listener != nil {
		s.listener.Close()
	}
	return s.tsServer.Close()
}

package middleware

import (
	"net"
	"net/url"
	"strings"

	"github.com/gin-contrib/cors"
)

// CORSConfig returns the cross-origin policy for the API.
//
// Explicit origins always win. Without them, production refuses every
// cross-origin request. Development allows only loopback origins, which is
// what a local Vite dev server needs. Reflecting any origin would let any
// web page the operator visits read tailnet devices, users, and the policy
// file from a tsflow on localhost or on the tailnet.
func CORSConfig(environment string, allowedOrigins []string) cors.Config {
	config := cors.DefaultConfig()
	switch {
	case len(allowedOrigins) > 0:
		config.AllowOrigins = allowedOrigins
	case strings.EqualFold(environment, "production"):
		config.AllowOriginFunc = func(string) bool { return false }
	default:
		config.AllowOriginFunc = isLoopbackOrigin
	}
	config.AllowCredentials = true
	config.AllowMethods = []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"}
	config.AllowHeaders = []string{"Origin", "Content-Type", "Accept", "Authorization"}
	return config
}

func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

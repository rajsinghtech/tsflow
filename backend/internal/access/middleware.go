package access

import (
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rajsinghtech/tsflow/backend/internal/config"
)

const identityKey = "tsflow.access.identity"

// Middleware enforces tailnet grants. A disabled config is a no-op so
// existing routes keep their current responses.
func Middleware(cfg config.Access, who WhoIsClient) gin.HandlerFunc {
	if !cfg.Enabled {
		return func(c *gin.Context) { c.Next() }
	}
	denied := newDenyLimiter(denyLogWindow)
	return func(c *gin.Context) {
		ident, err := Resolve(c.Request, cfg, who)
		if err != nil {
			logDenied(cfg, denied, c.Request, err)
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": publicError(err)})
			return
		}
		debugf(cfg, "login=%q name=%q node=%q groups=%q tailnets=%q", ident.Login, ident.Name, ident.Node, strings.Join(ident.Groups, ","), strings.Join(ident.Allow.TailnetIDs(), ","))
		c.Set(identityKey, ident)
		c.Next()
	}
}

// FromGin returns the viewer stored by Middleware.
// The second result is false when access control is off.
func FromGin(c *gin.Context) (Identity, bool) {
	if c == nil {
		return Identity{}, false
	}
	value, ok := c.Get(identityKey)
	if !ok {
		return Identity{}, false
	}
	ident, ok := value.(Identity)
	return ident, ok
}

func debugf(cfg config.Access, format string, args ...any) {
	if !cfg.Debug {
		return
	}
	log.Printf("DEBUG access: "+format, args...)
}

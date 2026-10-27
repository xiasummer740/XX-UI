package middleware

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/XiaSummer740/XX-UI/logger"

	"github.com/gin-gonic/gin"
)

// SameOrigin is a defence-in-depth CSRF guard for the cookie-authenticated panel
// routes.
//
// The session cookie already uses SameSite=Lax, which stops modern browsers from
// attaching it to cross-site POSTs, but that protection is browser-dependent and
// does not cover same-site-but-different-origin deployments (a sibling
// subdomain, or a proxy that rewrites the host). Rejecting state-changing
// requests whose Origin/Referer is not the panel itself closes that gap.
//
// Requests without any Origin/Referer header are allowed: those come from
// non-browser clients (curl, scripts, the Telegram bot) which never carry a
// victim's cookie. The API-key routes are not wrapped with this middleware.
func SameOrigin() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}

		origin := c.GetHeader("Origin")
		if origin == "" {
			origin = c.GetHeader("Referer")
		}
		if origin == "" {
			c.Next()
			return
		}

		u, err := url.Parse(origin)
		if err != nil || u.Host == "" {
			logger.Warning("blocked state-changing request with malformed Origin:", origin)
			c.AbortWithStatus(http.StatusForbidden)
			return
		}

		allowed := strings.EqualFold(u.Host, c.Request.Host)
		if !allowed {
			// Behind a reverse proxy the request Host can be the internal
			// address while the browser reports the public one.
			if fwd := c.GetHeader("X-Forwarded-Host"); fwd != "" {
				allowed = strings.EqualFold(u.Host, strings.TrimSpace(strings.Split(fwd, ",")[0]))
			}
		}
		if !allowed {
			logger.Warning("blocked cross-site", c.Request.Method, "from", origin, "to", c.Request.Host)
			c.AbortWithStatus(http.StatusForbidden)
			return
		}

		c.Next()
	}
}

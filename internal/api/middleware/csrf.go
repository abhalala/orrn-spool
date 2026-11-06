package middleware

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
)

var errCrossOrigin = errors.New("cross-origin request refused")

// CrossOriginProtection refuses state-changing browser requests that come
// from another origin, so a page elsewhere cannot use the operator's session
// cookie. It follows Go 1.25's http.CrossOriginProtection, with one added
// rule for ORRN's machine tokens:
//
//   - GET, HEAD and OPTIONS pass (they must not change state).
//   - A request with an ORRN machine token ("Authorization: Bearer v1:...")
//     passes: RequireAuth then authenticates by that header alone and ignores
//     any cookie, and browsers never add the header cross-origin without a
//     CORS preflight, which the spool does not grant. Any other Authorization
//     value is checked like a cookie request, because RequireAuth reads the
//     session cookie before it.
//   - Sec-Fetch-Site "same-origin" or "none" (typed or bookmarked) passes;
//     any other value ("same-site", "cross-site") is refused.
//   - Without Sec-Fetch-Site, an Origin header must name this host.
//   - With neither header the request is not from a modern browser (curl,
//     scripts) and passes.
//
// SameSite=Lax on the cookie already stops cross-site POSTs; this also
// stops same-site ones, such as another web app on a different port of the
// plant host, and older browsers without SameSite defaults.
func CrossOriginProtection() gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := checkCrossOrigin(c.Request); err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "cross-origin request refused"})
			return
		}
		c.Next()
	}
}

func checkCrossOrigin(r *http.Request) error {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}
	if isMachineAuth(r.Header.Get("Authorization")) {
		return nil
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		return nil
	case "":
	default:
		return errCrossOrigin
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.Host != r.Host {
		return errCrossOrigin
	}
	return nil
}

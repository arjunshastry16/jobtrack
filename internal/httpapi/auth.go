package httpapi

import (
	"mime"
	"net/http"
	"strings"
)

// Auth says who may use the app. The zero value lets nobody in.
//
// The app listens on loopback only and is reached through `tailscale serve`, which
// proxies each request from the tailnet with the caller's login in Tailscale-User-Login
// (after deleting any copy the caller sent). Requests without it, or with a login not
// on the list, are refused. See docs/adr/001-tailscale-identity-auth.md.
type Auth struct {
	// Logins are the Tailscale logins allowed in (compared ignoring case).
	Logins []string
	// Off lets every request through: local development only.
	Off bool
}

// headerLogin is the header tailscale serve sets to the caller's Tailscale login.
const headerLogin = "Tailscale-User-Login"

// tailscaleLogin returns the caller's Tailscale login from the request, or "" if there
// is none. tailscale serve may encode a non-ASCII value as an RFC 2047 encoded-word
// (=?utf-8?q?...?=), so it's decoded first.
func tailscaleLogin(r *http.Request) string {
	v := r.Header.Get(headerLogin)
	if decoded, err := new(mime.WordDecoder).DecodeHeader(v); err == nil {
		v = decoded
	}
	return strings.TrimSpace(v)
}

// requireLogin refuses requests without an allowed Tailscale login: 401 when there's no
// login at all (the request didn't come through tailscale serve, or came from a tagged
// device), 403 when the login isn't on the list. The health probes stay open, because
// deploy checks and monitors call them directly, without a login.
//
// It must not copy the request (see middleware.go), so it passes r on unchanged.
func requireLogin(auth Auth, next http.Handler) http.Handler {
	if auth.Off {
		return next
	}
	allowed := make(map[string]bool, len(auth.Logins))
	for _, l := range auth.Logins {
		allowed[strings.ToLower(l)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if probePaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		login := tailscaleLogin(r)
		switch {
		case login == "":
			writeError(w, http.StatusUnauthorized, codeUnauthenticated,
				"no Tailscale login on this request: open jobtrack through its tailnet address")
		case !allowed[strings.ToLower(login)]:
			writeError(w, http.StatusForbidden, codeForbidden, "this Tailscale login isn't allowed to use jobtrack")
		default:
			next.ServeHTTP(w, r)
		}
	})
}

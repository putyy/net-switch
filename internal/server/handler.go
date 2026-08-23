package server

import (
	"crypto/subtle"
	"io/fs"
	"net/http"
	"strings"
)

const SessionHeader = "X-Net-Switch-Token"

func newHandler(allowedHost, sessionToken string, files fs.FS, dependencies Dependencies) http.Handler {
	staticHandler := http.FileServer(http.FS(files))
	apiHandler := newAPIHandler(dependencies)
	expectedOrigin := "http://" + allowedHost

	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		setSecurityHeaders(response.Header())
		if request.Host != allowedHost {
			writeAPIError(response, http.StatusForbidden, "invalid_host", "Invalid request Host", "")
			return
		}

		if strings.HasPrefix(request.URL.Path, "/api/") {
			if !validSessionToken(request.Header.Get(SessionHeader), sessionToken) {
				writeAPIError(response, http.StatusUnauthorized, "invalid_session", "Invalid session token", "")
				return
			}
			origin := request.Header.Get("Origin")
			if origin != "" && origin != expectedOrigin {
				writeAPIError(response, http.StatusForbidden, "invalid_origin", "Invalid request Origin", "")
				return
			}
			if changesState(request.Method) && origin != expectedOrigin {
				writeAPIError(response, http.StatusForbidden, "origin_required", "State-changing requests must come from the dashboard", "")
				return
			}
			apiHandler.ServeHTTP(response, request)
			return
		}

		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			response.Header().Set("Allow", "GET, HEAD")
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		staticHandler.ServeHTTP(response, request)
	})
}

func setSecurityHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	header.Set("Cross-Origin-Opener-Policy", "same-origin")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

func validSessionToken(received, expected string) bool {
	return received != "" && expected != "" && subtle.ConstantTimeCompare([]byte(received), []byte(expected)) == 1
}

func changesState(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

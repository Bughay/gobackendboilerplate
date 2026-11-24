package lib

import "net/http"

// SecurityHeaders returns middleware that applies defensive headers to every
// response, including errors and CORS preflights, so no client or cache ever
// sees an unhardened reply.
//
// The headers are static, so there is nothing to configure and they are written
// once per request.
func SecurityHeaders() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := w.Header()

			// Stop a browser from re-interpreting a JSON body as HTML or script.
			header.Set("X-Content-Type-Options", "nosniff")
			// Legacy anti-clickjacking; the CSP frame-ancestors directive below
			// covers modern browsers.
			header.Set("X-Frame-Options", "DENY")
			// This is a JSON API: no subresources are legitimate and framing is not.
			header.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			// Keep request paths out of the Referer sent to third parties.
			header.Set("Referrer-Policy", "no-referrer")
			// Browsers ignore this over plain HTTP, so it is harmless locally and
			// effective when TLS terminates in front of the service.
			header.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			// Disable legacy cross-domain policy files read by Flash and PDF viewers.
			header.Set("X-Permitted-Cross-Domain-Policies", "none")

			// Deliberately NOT set: Cross-Origin-Resource-Policy and
			// Cross-Origin-Opener-Policy. CORP: same-origin would block the
			// cross-origin browser access this service's CORS config exists to
			// permit, so adding it would break real clients.

			// Headers are set before the handler runs, so they are present on
			// every status code the handler or a later middleware writes.
			next.ServeHTTP(w, r)
		})
	}
}

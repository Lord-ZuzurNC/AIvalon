package web

import "net/http"

// WithSecurityHeaders sets headers that cost nothing to always send
// and close off a couple of easy wins:
//   - X-Content-Type-Options: nosniff stops a browser from guessing a
//     different content type than what the server declared — relevant
//     mainly for /static/uploads/*, where the content type comes from
//     MIME-sniffing at upload time (see image_upload.go) rather than
//     the client-supplied filename, so there's no reason to let the
//     browser re-sniff it on the way out too.
//   - X-Frame-Options: DENY stops this app from being iframed by any
//     other page, local or remote — there's no legitimate reason for
//     AIvalon to render inside someone else's page.
func WithSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

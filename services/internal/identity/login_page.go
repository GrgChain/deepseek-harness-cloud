package identity

import (
	_ "embed"
	"html/template"
	"net/http"
)

//go:embed login.html
var loginHTML string

//go:embed login.css
var loginCSS string

//go:embed login.js
var loginJS string

var loginTemplate = template.Must(template.New("login").Parse(loginHTML))

func (s *AuthorizationServer) loginPage(w http.ResponseWriter, status int, request, csrf, message, email string) {
	// Chromium derives a form POST's Origin from the document's referrer
	// policy. no-referrer produces an opaque/null Origin even on this origin.
	// strict-origin preserves the exact origin without leaking authorize query.
	w.Header().Set("Referrer-Policy", "strict-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = loginTemplate.Execute(w, struct{ Request, CSRF, Error, Email string }{request, csrf, message, email})
}

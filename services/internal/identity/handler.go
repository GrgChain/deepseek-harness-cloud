package identity

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

type Handler struct{ service Authentication }

func NewHandler(service Authentication) *Handler { return &Handler{service: service} }
func cookieValue(r *http.Request, name string) string {
	values := r.CookiesNamed(name)
	if len(values) != 1 || len(values[0].Value) > 256 {
		return ""
	}
	return values[0].Value
}
func setCookie(w http.ResponseWriter, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}
func (h *Handler) Authenticate(r *http.Request) (Principal, error) {
	return h.service.Authenticate(r.Context(), cookieValue(r, SessionCookie))
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, message := 503, "DEPENDENCY_UNAVAILABLE", "身份服务暂不可用，请稍后重试"
	switch {
	case errors.Is(err, ErrUnauthenticated):
		status, code, message = 401, "UNAUTHENTICATED", "登录已失效或认证失败，请重新登录"
	case errors.Is(err, ErrForbidden):
		status, code, message = 403, "FORBIDDEN", "此账户未获平台授权，请联系管理员"
	case errors.Is(err, ErrCSRF):
		status, code, message = 403, "CSRF_INVALID", "请求验证失败，请刷新后重试"
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": message, "request_id": w.Header().Get("X-Request-ID")})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Referrer-Policy", "no-referrer")
	switch {
	case r.URL.Path == "/auth/login" && r.Method == http.MethodGet:
		values := r.URL.Query()["return_to"]
		if len(values) > 1 {
			WriteError(w, r, ErrForbidden)
			return
		}
		result, err := h.service.Login(r.Context(), cookieValue(r, LoginCookie), r.URL.Query().Get("return_to"))
		if err != nil {
			WriteError(w, r, err)
			return
		}
		setCookie(w, LoginCookie, result.BrowserSecret, 300)
		w.Header().Del("Content-Type")
		http.Redirect(w, r, result.URL, 302)
	case r.URL.Path == "/auth/callback" && r.Method == http.MethodGet:
		q := r.URL.Query()
		state, code, denial := q.Get("state"), q.Get("code"), q.Get("error")
		if len(q["state"]) != 1 || len(q["code"]) > 1 || len(q["error"]) > 1 || (code == "") == (denial == "") || len(code) > 4096 || len(denial) > 128 {
			WriteError(w, r, ErrUnauthenticated)
			return
		}
		result, err := h.service.Callback(r.Context(), state, cookieValue(r, LoginCookie), code, cookieValue(r, SessionCookie))
		setCookie(w, LoginCookie, "", -1)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		setCookie(w, SessionCookie, result.SessionSecret, int(time.Until(result.ExpiresAt).Seconds()))
		w.Header().Del("Content-Type")
		http.Redirect(w, r, result.ReturnPath, 303)
	case r.URL.Path == "/v1/me" && r.Method == http.MethodGet:
		p, err := h.Authenticate(r)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		_ = json.NewEncoder(w).Encode(p.CurrentUser())
	case r.URL.Path == "/auth/logout" && r.Method == http.MethodPost:
		p, err := h.Authenticate(r)
		if err != nil {
			WriteError(w, r, err)
			return
		}
		if err := h.service.Logout(r.Context(), p, r.Header.Get("X-CSRF-Token"), r.Header.Get("Origin")); err != nil {
			WriteError(w, r, err)
			return
		}
		setCookie(w, SessionCookie, "", -1)
		setCookie(w, LoginCookie, "", -1)
		w.WriteHeader(204)
	default:
		w.WriteHeader(405)
	}
}

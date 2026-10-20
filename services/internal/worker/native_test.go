package worker

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNativeExchangeBindsAuthorityAndNeverFollowsRedirects(t *testing.T) {
	host := "i-test.harness.test"
	sum := sha256.Sum256([]byte(host))
	name := "dsh-auth-" + base64.RawURLEncoding.EncodeToString(sum[:])
	for _, scenario := range []string{"valid", "redirect", "domain", "no-cookie", "foreign-cookie", "bad-root", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			var called, leaked bool
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
			defer target.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host != host || r.Header.Get("Authorization") != "" {
					t.Error("native authority changed")
				}
				if r.URL.RawQuery != "" {
					if called || r.URL.Query().Get("token") != "synthetic-launch-token" {
						t.Error("invalid private exchange")
					}
					called = true
					cookie := &http.Cookie{Name: name, Value: "v1.synthetic.signature", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 2592000, Expires: time.Now().Add(30 * 24 * time.Hour)}
					if scenario == "domain" {
						cookie.Domain = ".harness.test"
					}
					if scenario == "foreign-cookie" {
						cookie.Name = "dsh-auth-foreign"
					}
					if scenario != "no-cookie" {
						http.SetCookie(w, cookie)
					}
					if scenario == "duplicate" {
						http.SetCookie(w, cookie)
					}
					location := "./"
					if scenario == "redirect" {
						location = target.URL + "/?token=synthetic-launch-token"
					}
					w.Header().Set("Location", location)
					w.WriteHeader(303)
					return
				}
				if r.Header.Get("Cookie") == "" {
					w.WriteHeader(401)
					return
				}
				if scenario == "bad-root" {
					w.WriteHeader(401)
					return
				}
				if r.Header.Get("Cookie") != name+"=v1.synthetic.signature" {
					t.Error("wrong server-held native cookie")
				}
				w.WriteHeader(200)
			}))
			defer server.Close()
			cookie, expires, err := exchangeNative(context.Background(), server.URL, host, "synthetic-launch-token")
			if scenario == "valid" {
				if err != nil || !strings.HasPrefix(string(cookie), name+"=") || !expires.After(time.Now()) || expires.After(time.Now().Add(8*time.Hour)) {
					t.Fatalf("native exchange failed: %v", err)
				}
			} else if !errors.Is(err, ErrNativeUnavailable) {
				t.Fatalf("invalid %s exchange accepted: %v", scenario, err)
			}
			if leaked {
				t.Fatal("native redirect leaked the launch token")
			}
			if err != nil && strings.Contains(err.Error(), "synthetic") {
				t.Fatal("private credential entered error")
			}
		})
	}
}

package identity

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/jackc/pgx/v5"
)

// Browser forms must not outlive the BFF transaction that will accept their code.
// Protocol clients without an HTML navigation retain the standard OIDC flow.
func (s *AuthorizationServer) browserLogin(ctx context.Context, state, browser string) (string, bool, error) {
	if len(state) < 32 || len(state) > 256 || len(browser) != 43 {
		return "", false, nil
	}
	var path string
	var active bool
	err := s.pool.QueryRow(ctx, `SELECT return_path,consumed_at IS NULL AND expires_at>clock_timestamp()
 FROM oidc_transactions WHERE state_sha256=$1 AND browser_sha256=$2`, digest(state), digest(browser)).Scan(&path, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	path, err = ReturnPath(path)
	return path, active && err == nil, nil
}

func (s *AuthorizationServer) browserFormState(ctx context.Context, id, browser, csrf string) (string, error) {
	if !uuidPattern.MatchString(id) || len(browser) != 43 || len(csrf) != 43 {
		return "", nil
	}
	var state string
	err := s.pool.QueryRow(ctx, `SELECT state FROM identity_authorizations
 WHERE id=$1 AND browser_sha256=$2 AND csrf_sha256=$3 AND consumed_at IS NULL
 AND expires_at>clock_timestamp() AND attempts<10`, id, digest(browser), digest(csrf)).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return state, err
}

func restartBrowserLogin(w http.ResponseWriter, r *http.Request, path string) {
	target := "/auth/login"
	if path != "" {
		target += "?return_to=" + url.QueryEscape(path)
	}
	setCookie(w, ProviderCookie, "", -1)
	w.Header().Del("Content-Type")
	http.Redirect(w, r, target, http.StatusSeeOther)
}

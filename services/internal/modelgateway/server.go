package modelgateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/modelendpoint"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
)

type Server struct {
	pool                   *pgxpool.Pool
	cipher                 *security.SecretCipher
	environment            string
	master                 [32]byte
	client                 *http.Client
	slots                  chan struct{}
	maintenance            bool
	customBases            []string
	responseMu             sync.Mutex
	responseReasoning      map[string]cachedResponseReasoning
	responseReasoningBytes int
}

func NewServer(pool *pgxpool.Pool, cipher *security.SecretCipher, environment string, master []byte, maintenance bool, customBases ...string) (*Server, error) {
	if !modelendpoint.ValidBases(customBases) || pool == nil || cipher == nil || environment == "" || len(master) < 32 || len(master) > 4096 {
		return nil, errProtocol
	}
	return &Server{pool: pool, cipher: cipher, environment: environment, master: sha256.Sum256(master), customBases: append([]string(nil), customBases...), client: providerClient(customBases...), slots: make(chan struct{}, 32), maintenance: maintenance, responseReasoning: map[string]cachedResponseReasoning{}}, nil
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"type": code, "message": "Model request unavailable"}})
}
func decode(w http.ResponseWriter, r *http.Request, value any) error {
	if r.Header.Get("Content-Type") != "application/json" {
		return errProtocol
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		return errProtocol
	}
	return nil
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	if r.Method == "GET" && r.URL.Path == "/readyz" {
		check, stop := context.WithTimeout(ctx, 2*time.Second)
		defer stop()
		if s.pool.Ping(check) != nil {
			writeError(w, 503, "UNAVAILABLE")
		} else {
			writeJSON(w, 200, map[string]string{"status": "ready"})
		}
		return
	}
	if s.maintenance {
		writeError(w, 503, "MAINTENANCE")
		return
	}
	if len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		writeError(w, 401, "UNAUTHORIZED")
		return
	}
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(key) < 16 || len(key) > 4096 || strings.ContainsAny(key, " \t\r\n") {
		writeError(w, 401, "UNAUTHORIZED")
		return
	}
	digest := sha256.Sum256([]byte(key))
	master := subtle.ConstantTimeCompare(digest[:], s.master[:]) == 1
	if r.URL.Path != "/v1/models" && r.URL.Path != "/v1/messages" {
		if !master {
			writeError(w, 403, "FORBIDDEN")
			return
		}
		s.management(w, r)
		return
	}
	// The management credential cannot execute inference or enumerate user models.
	if master {
		writeError(w, 403, "FORBIDDEN")
		return
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		writeError(w, 429, "CAPACITY_EXCEEDED")
		return
	}
	route, providerAccountKey, err := s.authorize(ctx, digest[:])
	if err != nil {
		writeError(w, 401, "UNAUTHORIZED")
		return
	}
	defer clear(providerAccountKey)
	accountScope := r.URL.Query().Get("scope") == "provider-account"
	if r.Method == "GET" && r.URL.Path == "/v1/models" && accountScope {
		models, e := s.accountModels(ctx, digest[:], route)
		if e != nil {
			writeError(w, 503, "UNAVAILABLE")
			return
		}
		writeJSON(w, 200, map[string]any{"object": "list", "data": models})
		return
	}
	if r.Method == "GET" && r.URL.Path == "/v1/models" {
		writeJSON(w, 200, map[string]any{"object": "list", "data": []any{map[string]any{"id": route.Alias, "object": "model", "owned_by": "platform"}}})
		return
	}
	if r.Method != "POST" || r.URL.Path != "/v1/messages" {
		writeError(w, 405, "METHOD_NOT_ALLOWED")
		return
	}
	var request Request
	if decode(w, r, &request) != nil {
		writeError(w, 400, "INVALID_REQUEST")
		return
	}
	if accountScope {
		var selectedKey []byte
		route, selectedKey, err = s.authorizeRoute(ctx, digest[:], request.Model)
		if err != nil || request.Model == "" {
			clear(selectedKey)
			writeError(w, 403, "MODEL_NOT_ALLOWED")
			return
		}
		defer clear(selectedKey)
		providerAccountKey = selectedKey
	}
	if request.validate(route) != nil {
		writeError(w, 400, "INVALID_REQUEST")
		return
	}
	if usesResponses(route) {
		request.responseState = &responseState{}
		if s.loadResponseReasoning(route, &request) != nil {
			clearRequestReasoning(&request)
			writeError(w, 400, "INVALID_REQUEST")
			return
		}
		defer clearRequestReasoning(&request)
	}
	var allowed bool
	err = s.pool.QueryRow(ctx, `UPDATE model_gateway_keys SET window_started=CASE WHEN window_started<=clock_timestamp()-interval '1 minute' THEN clock_timestamp() ELSE window_started END,requests=CASE WHEN window_started<=clock_timestamp()-interval '1 minute' THEN 1 ELSE requests+1 END WHERE key_hash=$1 AND revoked_at IS NULL AND (window_started<=clock_timestamp()-interval '1 minute' OR requests<60) RETURNING true`, digest[:]).Scan(&allowed)
	if err != nil || !allowed {
		w.Header().Set("Retry-After", "60")
		writeError(w, 429, "RATE_LIMITED")
		return
	}
	tracked := &streamWriter{ResponseWriter: w}
	forwardErr := forward(ctx, tracked, s.client, route, request, providerAccountKey)
	if forwardErr == nil && request.responseState != nil {
		s.storeResponseReasoning(route, request.responseState)
	}
	if forwardErr != nil {
		if tracked.written {
			e := events{w: tracked}
			_ = e.emit("error", map[string]any{"error": map[string]string{"type": "MODEL_PROTOCOL_UNAVAILABLE", "message": "Model stream interrupted"}})
		} else {
			writeError(w, 502, "MODEL_PROTOCOL_UNAVAILABLE")
		}
	}
}

type streamWriter struct {
	http.ResponseWriter
	written bool
}

func (w *streamWriter) WriteHeader(status int) {
	w.written = true
	w.ResponseWriter.WriteHeader(status)
}
func (w *streamWriter) Write(data []byte) (int, error) {
	w.written = true
	return w.ResponseWriter.Write(data)
}
func (w *streamWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) authorize(ctx context.Context, hash []byte) (Route, []byte, error) {
	return s.authorizeRoute(ctx, hash, "")
}

// An explicit account request may use only the anchor credential's own account.
// Probe/management calls retain their single-route contract.
func (s *Server) authorizeRoute(ctx context.Context, hash []byte, alias string) (Route, []byte, error) {
	var route Route
	var raw, frozen []byte
	var env security.Envelope
	var secret string
	var bound int64
	check, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := s.pool.QueryRow(check, `SELECT r.id::text,r.alias,r.org_id::text,r.owner_id::text,r.connection_id::text,r.connection_version,r.parameters,v.manifest,v.manifest->'catalog'->'response_models',(v.manifest->'catalog'->>'max_output_tokens')::bigint,v.manifest->'catalog'->>'model',(v.manifest->'catalog'->>'context_window')::bigint,ps.id::text,ps.bound_version,ps.key_id,ps.nonce,ps.ciphertext
 FROM model_gateway_keys k JOIN model_gateway_routes r ON
 (($2='' AND r.id=k.route_id) OR ($2<>'' AND r.alias=$2 AND EXISTS (
 SELECT 1 FROM model_gateway_routes anchor
 JOIN user_model_connections ah ON ah.id=anchor.connection_id AND ah.org_id=anchor.org_id AND ah.owner_id=anchor.owner_id
 JOIN personal_provider_accounts a ON a.id=ah.provider_account_id AND a.org_id=ah.org_id AND a.owner_id=ah.owner_id
 JOIN user_model_connections target ON target.id=r.connection_id AND target.org_id=r.org_id AND target.owner_id=r.owner_id AND target.provider_account_id=a.id
 WHERE anchor.id=k.route_id AND anchor.enabled AND ah.enabled AND ah.archived_at IS NULL AND a.status='ACTIVE'
 AND r.org_id=anchor.org_id AND r.owner_id=anchor.owner_id)))
 JOIN user_model_connection_versions v ON v.org_id=r.org_id AND v.owner_id=r.owner_id AND v.connection_id=r.connection_id AND v.version=r.connection_version
 JOIN user_model_connections h ON h.id=v.connection_id AND h.org_id=v.org_id AND h.owner_id=v.owner_id
 JOIN external_provider_secrets ps ON ps.id=v.provider_secret_id AND ps.org_id=v.org_id AND ps.owner_id=v.owner_id AND ps.connection_id=v.connection_id
 JOIN secrets sec ON sec.id=v.runtime_credential_ref AND sec.org_id=v.org_id
 JOIN model_configurations c ON c.id=v.connection_id AND c.version=v.version AND c.org_id=v.org_id
 JOIN model_catalog_entries e ON e.id=v.catalog_entry_id AND e.org_id=v.org_id JOIN model_providers p ON p.id=e.provider_id
 JOIN memberships m ON m.org_id=v.org_id AND m.user_id=v.owner_id JOIN users u ON u.id=m.user_id JOIN organizations o ON o.id=m.org_id
 WHERE k.key_hash=$1 AND k.revoked_at IS NULL AND r.enabled AND h.enabled AND h.archived_at IS NULL AND v.sync_status='SYNCED'
 AND sec.revoked_at IS NULL AND ps.revoked_at IS NULL AND c.revoked_at IS NULL AND c.status<>'REVOKED' AND e.enabled AND p.enabled
 AND m.status='ACTIVE' AND u.status='ACTIVE' AND o.status='ACTIVE' AND public.hc_has_permission(v.org_id,v.owner_id,'personal-model:write')
 AND ($2='' OR (NOT h.provider_retired AND (v.version=h.current_version OR r.id=k.route_id)
 AND EXISTS(SELECT 1 FROM model_validations mv WHERE mv.config_id=c.id AND mv.config_version=c.version AND mv.status='HEALTHY' AND mv.checked_at<=clock_timestamp() AND mv.expires_at>clock_timestamp())
 AND EXISTS(SELECT 1 FROM model_gateway_checks gc WHERE gc.connection_id=v.connection_id AND gc.connection_version=v.version AND gc.status='HEALTHY' AND gc.checked_at<=clock_timestamp() AND gc.expires_at>clock_timestamp())))`, hash, alias).Scan(&route.ID, &route.Alias, &route.Org, &route.Owner, &route.Connection, &route.Version, &raw, &frozen, &route.ResponseModels, &route.MaxOutputTokens, &route.Name, &route.ContextWindow, &secret, &bound, &env.KeyID, &env.Nonce, &env.Ciphertext)
	approved, freezeErr := frozenParameters(frozen)
	if err != nil || json.Unmarshal(raw, &route.Params) != nil || !approvedParams(route.Params, s.customBases...) || freezeErr != nil || !reflect.DeepEqual(route.Params, approved) {
		return route, nil, errProtocol
	}
	key, err := s.cipher.Decrypt(env, security.Context{Environment: s.environment, OrganizationID: route.Org, RecordID: fmt.Sprintf("%s/%s/%s/%d", route.Owner, route.Connection, secret, bound), Purpose: "provider-key"})
	return route, key, err
}
func (s *Server) management(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	switch r.Method + " " + r.URL.Path {
	case "GET /v2/model/info":
		id := r.URL.Query().Get("modelId")
		var alias string
		var raw []byte
		err := s.pool.QueryRow(ctx, `SELECT alias,parameters FROM model_gateway_routes WHERE id::text=$1 AND enabled`, id).Scan(&alias, &raw)
		data := []any{}
		if err == nil {
			data = append(data, map[string]any{"model_name": alias, "provider_params": json.RawMessage(raw), "model_info": map[string]string{"id": id}})
		}
		if err != nil && err != pgx.ErrNoRows {
			writeError(w, 503, "UNAVAILABLE")
			return
		}
		writeJSON(w, 200, map[string]any{"data": data})
	case "POST /model/new":
		s.createRoute(ctx, w, r)
	case "POST /model/delete":
		var v struct {
			ID string `json:"id"`
		}
		if decode(w, r, &v) != nil {
			writeError(w, 400, "INVALID_REQUEST")
			return
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			writeError(w, 503, "UNAVAILABLE")
			return
		}
		defer tx.Rollback(context.Background())
		if _, err = tx.Exec(ctx, `UPDATE model_gateway_routes SET enabled=false WHERE id::text=$1`, v.ID); err == nil {
			_, err = tx.Exec(ctx, `UPDATE model_gateway_keys SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE route_id::text=$1`, v.ID)
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		if err != nil {
			writeError(w, 503, "UNAVAILABLE")
			return
		}
		writeJSON(w, 200, map[string]bool{"deleted": true})
	case "GET /key/info":
		hash, err := hex.DecodeString(r.URL.Query().Get("key"))
		if err != nil || len(hash) != 32 {
			writeError(w, 400, "INVALID_REQUEST")
			return
		}
		var alias string
		err = s.pool.QueryRow(ctx, `SELECT r.alias FROM model_gateway_keys k JOIN model_gateway_routes r ON r.id=k.route_id WHERE k.key_hash=$1 AND k.revoked_at IS NULL AND r.enabled`, hash).Scan(&alias)
		if err != nil {
			if err == pgx.ErrNoRows {
				writeError(w, 404, "NOT_FOUND")
			} else {
				writeError(w, 503, "UNAVAILABLE")
			}
			return
		}
		writeJSON(w, 200, map[string]any{"info": map[string]any{"models": []string{alias}, "allowed_routes": []string{"/v1/messages", "/v1/models"}, "status": "active", "blocked": false}})
	case "POST /key/generate":
		s.createKey(ctx, w, r)
	case "POST /key/delete":
		var v struct {
			Keys []string `json:"keys"`
		}
		if decode(w, r, &v) != nil || len(v.Keys) != 1 {
			writeError(w, 400, "INVALID_REQUEST")
			return
		}
		hash := sha256.Sum256([]byte(v.Keys[0]))
		if _, err := s.pool.Exec(ctx, `UPDATE model_gateway_keys SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE key_hash=$1`, hash[:]); err != nil {
			writeError(w, 503, "UNAVAILABLE")
			return
		}
		writeJSON(w, 200, map[string]bool{"deleted": true})
	default:
		writeError(w, 404, "NOT_FOUND")
	}
}
func (s *Server) createRoute(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var v struct {
		Alias  string         `json:"model_name"`
		Params map[string]any `json:"provider_params"`
		Info   struct {
			ID string `json:"id"`
		} `json:"model_info"`
	}
	if decode(w, r, &v) != nil {
		writeError(w, 400, "INVALID_REQUEST")
		return
	}
	supplied, _ := v.Params["api_key"].(string)
	delete(v.Params, "api_key")
	if supplied == "" || !approvedParams(v.Params, s.customBases...) {
		writeError(w, 400, "INVALID_ROUTE")
		return
	}
	var org, owner, connection, secret string
	var version, bound int64
	var env security.Envelope
	var frozen []byte
	err := s.pool.QueryRow(ctx, `SELECT v.org_id::text,v.owner_id::text,v.connection_id::text,v.version,v.manifest,ps.id::text,ps.bound_version,ps.key_id,ps.nonce,ps.ciphertext FROM model_sync_operations op JOIN user_model_connection_versions v ON v.connection_id=op.connection_id AND v.version=op.connection_version JOIN external_provider_secrets ps ON ps.id=v.provider_secret_id WHERE op.remote_model_id=$1 AND op.action='UPSERT' AND v.route_alias=$2 AND ps.revoked_at IS NULL`, v.Info.ID, v.Alias).Scan(&org, &owner, &connection, &version, &frozen, &secret, &bound, &env.KeyID, &env.Nonce, &env.Ciphertext)
	var manifest struct {
		Prefix     string
		Parameters map[string]any
		Catalog    struct {
			Model   string `json:"model"`
			APIBase string `json:"api_base"`
		}
	}
	if err != nil || json.Unmarshal(frozen, &manifest) != nil {
		writeError(w, 409, "ROUTE_NOT_APPROVED")
		return
	}
	if manifest.Parameters == nil {
		manifest.Parameters = map[string]any{}
	}
	manifest.Parameters["model"] = manifest.Prefix + "/" + manifest.Catalog.Model
	manifest.Parameters["api_base"] = manifest.Catalog.APIBase
	manifest.Parameters["max_retries"] = float64(0)
	expected, _ := json.Marshal(manifest.Parameters)
	params, _ := json.Marshal(v.Params)
	if !bytes.Equal(expected, params) {
		writeError(w, 409, "ROUTE_NOT_APPROVED")
		return
	}
	key, err := s.cipher.Decrypt(env, security.Context{Environment: s.environment, OrganizationID: org, RecordID: fmt.Sprintf("%s/%s/%s/%d", owner, connection, secret, bound), Purpose: "provider-key"})
	defer clear(key)
	if err != nil || subtle.ConstantTimeCompare(key, []byte(supplied)) != 1 {
		writeError(w, 409, "CREDENTIAL_NOT_APPROVED")
		return
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO model_gateway_routes(id,org_id,owner_id,connection_id,connection_version,alias,parameters) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(id) DO NOTHING`, v.Info.ID, org, owner, connection, version, v.Alias, params)
	if err != nil {
		writeError(w, 409, "ROUTE_CONFLICT")
		return
	}
	var matches bool
	err = s.pool.QueryRow(ctx, `SELECT enabled AND org_id=$2::uuid AND owner_id=$3::uuid AND connection_id=$4::uuid AND connection_version=$5 AND alias=$6 AND parameters=$7::jsonb FROM model_gateway_routes WHERE id=$1`, v.Info.ID, org, owner, connection, version, v.Alias, params).Scan(&matches)
	if err != nil || !matches {
		writeError(w, 409, "ROUTE_CONFLICT")
		return
	}
	writeJSON(w, 200, map[string]string{"id": v.Info.ID})
}
func (s *Server) createKey(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var v struct {
		Key    string   `json:"key"`
		Models []string `json:"models"`
		Routes []string `json:"allowed_routes"`
		Type   string   `json:"key_type"`
		Invite bool     `json:"send_invite_email"`
		RPM    int      `json:"rpm_limit"`
	}
	if decode(w, r, &v) != nil || len(v.Key) < 32 || len(v.Key) > 4096 || len(v.Models) != 1 || len(v.Routes) != 2 || v.Routes[0] != "/v1/messages" || v.Routes[1] != "/v1/models" || v.Invite || v.Type != "default" || v.RPM != 60 {
		writeError(w, 400, "INVALID_KEY_SCOPE")
		return
	}
	var id, org, credential string
	var env security.Envelope
	err := s.pool.QueryRow(ctx, `SELECT r.id::text,r.org_id::text,sec.id::text,sec.key_id,sec.nonce,sec.ciphertext FROM model_gateway_routes r JOIN user_model_connection_versions v ON v.connection_id=r.connection_id AND v.version=r.connection_version JOIN secrets sec ON sec.id=v.runtime_credential_ref AND sec.org_id=v.org_id WHERE r.alias=$1 AND r.enabled AND sec.revoked_at IS NULL`, v.Models[0]).Scan(&id, &org, &credential, &env.KeyID, &env.Nonce, &env.Ciphertext)
	if err != nil {
		writeError(w, 409, "ROUTE_NOT_APPROVED")
		return
	}
	key, err := s.cipher.Decrypt(env, security.Context{Environment: s.environment, OrganizationID: org, RecordID: credential, Purpose: "model-key"})
	defer clear(key)
	if err != nil || subtle.ConstantTimeCompare(key, []byte(v.Key)) != 1 {
		writeError(w, 409, "CREDENTIAL_NOT_APPROVED")
		return
	}
	hash := sha256.Sum256(key)
	_, err = s.pool.Exec(ctx, `INSERT INTO model_gateway_keys(key_hash,route_id) VALUES($1,$2) ON CONFLICT(key_hash) DO NOTHING`, hash[:], id)
	var valid bool
	if err == nil {
		err = s.pool.QueryRow(ctx, `SELECT route_id=$2::uuid AND revoked_at IS NULL FROM model_gateway_keys WHERE key_hash=$1`, hash[:], id).Scan(&valid)
	}
	if err != nil || !valid {
		writeError(w, 409, "KEY_CONFLICT")
		return
	}
	writeJSON(w, 200, map[string]bool{"created": true})
}

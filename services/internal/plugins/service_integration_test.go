package plugins

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/httpserver"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type pluginProvider struct{ subject, state, nonce string }

func (p *pluginProvider) Authorize(state, nonce, verifier string) string {
	p.state, p.nonce = state, nonce
	return "https://idp.example/auth"
}
func (p *pluginProvider) Exchange(context.Context, string, string) (identity.VerifiedIdentity, error) {
	return identity.VerifiedIdentity{Issuer: "https://idp.example", Subject: p.subject, Nonce: p.nonce}, nil
}

type resolveFunc func(context.Context, string) (Plugin, error)

func (f resolveFunc) Resolve(ctx context.Context, spec string) (Plugin, error) { return f(ctx, spec) }

func pluginProblem(err error, code string) bool {
	var p *webapi.Problem
	return errors.As(err, &p) && p.Code == code
}

func personalPluginArchive(t *testing.T, name string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	manifest, _ := json.Marshal(map[string]any{"name": name, "version": "1.2.3", "description": "离线服务端元数据", "dsh": map[string]any{"bundle": map[string]string{"patch": "patch.json"}}})
	for _, item := range []struct {
		name string
		data []byte
	}{{"package/package.json", manifest}, {"package/patch.json", []byte(`{}`)}} {
		if err := archive.WriteHeader(&tar.Header{Name: item.name, Mode: 0600, Size: int64(len(item.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(item.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func editPluginMetadata(v Plugin) EditInput {
	return EditInput{Name: v.Name, Summary: v.Summary, Description: v.Description, Author: v.Author, Version: v.Version, Category: v.Category, Keywords: v.Keywords, Repository: v.Repository, Documentation: v.Documentation, Compatibility: v.Compatibility, CheckedAt: v.CheckedAt, SourceNote: v.SourceNote, ExpectedRevision: v.Revision}
}

func TestPersonalPluginsWithPostgres(t *testing.T) {
	filename := os.Getenv("HC_TEST_DB_DSN_FILE")
	if filename == "" {
		t.Skip("isolated PostgreSQL required")
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal("cannot read database connection file")
	}
	u, err := url.Parse(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal("invalid database connection file")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	u.Path = "/postgres"
	admin, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatal("isolated database administrator unavailable")
	}
	database := "hc_test_plugins_" + strings.ReplaceAll(webapi.ID(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{database}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	var root, app *pgxpool.Pool
	t.Cleanup(func() {
		if app != nil {
			app.Close()
		}
		if root != nil {
			root.Close()
		}
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = admin.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{database}.Sanitize())
		_ = admin.Close(cleanup)
	})
	u.Path = "/" + database
	if err = db.Apply(ctx, u.String()); err != nil {
		t.Fatal(err)
	}
	root, err = pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := root.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = devlocal.GrantAPI(ctx, tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(u.String())
	if err != nil {
		t.Fatal(err)
	}
	config.AfterConnect = func(ctx context.Context, c *pgx.Conn) error { _, err := c.Exec(ctx, "SET ROLE hc_api"); return err }
	app, err = pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	cipher, _ := security.NewSecretCipher("test-key", bytes.Repeat([]byte{7}, 32))
	org := webapi.ID()
	if err = identity.ApplyMapping(ctx, root, identity.Mapping{SchemaVersion: 1, OrganizationID: org, Members: []identity.MemberMapping{
		{Issuer: "https://idp.example", Subject: "owner", Role: "MEMBER", WorkspaceOwnerID: webapi.ID()},
		{Issuer: "https://idp.example", Subject: "admin", Role: "ADMIN", WorkspaceOwnerID: webapi.ID()},
	}}, "plugin-test"); err != nil {
		t.Fatal(err)
	}
	login := func(organization, subject string) (identity.Principal, string, *identity.Service) {
		t.Helper()
		provider := &pluginProvider{subject: subject}
		auth := identity.NewService(app, provider, cipher, "test", organization, "https://portal.example")
		l, err := auth.Login(ctx, "", "/app/plugins")
		if err != nil {
			t.Fatal(err)
		}
		result, err := auth.Callback(ctx, provider.state, l.BrowserSecret, provider.state, "")
		if err != nil {
			t.Fatal(err)
		}
		principal, err := auth.Authenticate(ctx, result.SessionSecret)
		if err != nil {
			t.Fatal(err)
		}
		return principal, result.SessionSecret, auth
	}
	owner, ownerSecret, auth := login(org, "owner")
	other, otherSecret, _ := login(org, "admin")
	if !owner.Has("plugins:manage") || !other.Has("plugins:manage") {
		t.Fatal("fresh built-in roles lack plugin management")
	}
	metadata := func(spec string) Plugin {
		index := strings.LastIndex(spec, "@")
		name, version := spec, "1.2.3"
		if index > 0 {
			name, version = spec[:index], spec[index+1:]
		}
		return Plugin{Name: "经过核对的插件", PackageName: name, Version: version, Author: "package-author", Category: "集成扩展", Summary: "服务端摘要", Description: "服务端说明", Keywords: []string{"dsh"}, Repository: "https://github.com/example/plugin", Documentation: "https://github.com/example/plugin#readme", Compatibility: "需要 DSH 检查依赖", CheckedAt: "2026-11-02", SourceNote: "包信息及 DSH bundle 声明"}
	}
	var resolved []string
	var offlineID string
	s := NewService(app, cipher, "test", false).WithRegistry(resolveFunc(func(_ context.Context, spec string) (Plugin, error) {
		resolved = append(resolved, spec)
		return metadata(spec), nil
	}))
	preview, err := s.Preview(ctx, owner, "example-dsh-plugin")
	if err != nil || preview.Version != "1.2.3" || preview.ID != "" || !preview.Custom || preview.SourceNote != "包信息及 DSH bundle 声明" {
		t.Fatal("invalid preview", preview, err)
	}
	items, err := s.List(ctx, owner)
	if err != nil || len(items) != 5 {
		t.Fatal("preview saved an entry", items, err)
	}
	for _, item := range items {
		if item.Custom || item.Revision < 1 || item.AdditionMethod != "online" {
			t.Fatal("database default did not have catalog metadata", item)
		}
	}
	saved, err := s.Create(ctx, owner, preview.PackageName, preview.Version, "开发工具", "plugin-add")
	if err != nil {
		t.Fatal(err)
	}
	initialOnlineID := saved.ID
	if len(resolved) != 2 || resolved[1] != "example-dsh-plugin@1.2.3" || saved.Source != "community" || saved.Category != "开发工具" || !saved.Custom {
		t.Fatal("save did not re-resolve the exact version", resolved, saved)
	}
	items, err = s.List(ctx, owner)
	if err != nil || len(items) != 6 || items[0].ID != saved.ID || items[0].Summary != "服务端摘要" || items[0].CheckedAt != "2026-11-02" || items[0].Revision != 1 || items[0].SourceNote != "包信息及 DSH bundle 声明" {
		t.Fatal("saved plugin did not persist", items, err)
	}
	items, err = s.List(ctx, other)
	if err != nil || len(items) != 5 {
		t.Fatal("personal plugins leaked to another owner or administrator", items, err)
	}
	for _, item := range items {
		if item.Custom {
			t.Fatal("another owner's entry leaked", item)
		}
	}
	foreign := owner
	foreign.OrganizationID = webapi.ID()
	if _, err = s.List(ctx, foreign); !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("principal from another organization read plugins", err)
	}
	if _, err = s.Create(ctx, owner, saved.PackageName, saved.Version, saved.Category, "duplicate"); !pluginProblem(err, "PLUGIN_EXISTS") {
		t.Fatal("duplicate package was accepted", err)
	}
	for _, name := range []string{"@openviking/dsh-memory-plugin", "@linxin666/dsh-ssh", "dshmarket"} {
		if _, err = s.Create(ctx, owner, name, "1.0.0", "集成扩展", "recommended"); !pluginProblem(err, "PLUGIN_EXISTS") {
			t.Fatal("recommended plugin was duplicated", name, err)
		}
	}
	var audits int
	if err = root.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE org_id=$1 AND owner_id=$2 AND action='PLUGIN_ADD'`, org, owner.UserID).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("missing or duplicate plugin audit", audits, err)
	}
	t.Run("save requires a bare package and fixed version before registry access", func(t *testing.T) {
		calls := len(resolved)
		for _, input := range [][3]string{
			{"valid-plugin", "latest", "开发工具"},
			{"valid-plugin@1.0.0", "1.0.0", "开发工具"},
			{"valid-plugin", "^1.0.0", "开发工具"},
			{"valid-plugin", "1.0.0", "其他分类"},
			{"@deepseek-ai/dsh-plugin", "1.0.0", "开发工具"},
		} {
			if _, err := s.Create(ctx, owner, input[0], input[1], input[2], "invalid"); !pluginProblem(err, "PLUGIN_INVALID") {
				t.Fatal("invalid save accepted", input, err)
			}
		}
		if len(resolved) != calls {
			t.Fatal("invalid save reached registry")
		}
		boundary := NewService(app, cipher, "test", false).WithRegistry(resolveFunc(func(_ context.Context, spec string) (Plugin, error) {
			v := metadata(spec)
			v.Name = v.PackageName
			return v, nil
		}))
		name, version := "-"+strings.Repeat("a", 213), "1.0.0-"+strings.Repeat("x", 122)
		if _, err := boundary.Create(ctx, owner, name, version, "开发工具", "boundary"); err != nil {
			t.Fatal("valid registry length boundary could not persist", err)
		}
	})
	t.Run("HTTP routes enforce CSRF and persist only decoded input", func(t *testing.T) {
		server := httpserver.New("api", nil)
		server.WithIdentity(identity.NewHandler(auth))
		server.WithBusiness(httpserver.BusinessMux{Plugins: NewHandler(s, func(context.Context, string, int) error { return nil })}, "https://portal.example")
		request := func(method, path, body, origin, csrf string) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
			r.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: ownerSecret})
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", origin)
			r.Header.Set("X-CSRF-Token", csrf)
			w := httptest.NewRecorder()
			server.ServeHTTP(w, r)
			return w
		}
		calls := len(resolved)
		for _, values := range [][2]string{{"https://portal.example", ""}, {"https://other.example", owner.CSRFToken}} {
			if w := request(http.MethodPost, "/v1/plugins:preview", `{"package_name":"http-dsh-plugin"}`, values[0], values[1]); w.Code != 403 || !strings.Contains(w.Body.String(), "CSRF_INVALID") {
				t.Fatal("invalid CSRF accepted", w.Code, w.Body.String())
			}
		}
		if len(resolved) != calls {
			t.Fatal("CSRF failure reached registry")
		}
		if w := request(http.MethodPost, "/v1/plugins:preview", `{"package_name":"http-dsh-plugin"}`, "https://portal.example", owner.CSRFToken); w.Code != 200 || !strings.Contains(w.Body.String(), `"package_name":"http-dsh-plugin"`) {
			t.Fatal("valid plugin preview route failed", w.Code, w.Body.String())
		}
		if w := request(http.MethodPost, "/v1/plugins", `{"package_name":"http-dsh-plugin","version":"1.2.3","category":"会话与记忆"}`, "https://portal.example", owner.CSRFToken); w.Code != 201 || !strings.Contains(w.Body.String(), `"category":"会话与记忆"`) {
			t.Fatal("valid plugin save route failed", w.Code, w.Body.String())
		}
		if w := request(http.MethodGet, "/v1/plugins", "", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"package_name":"http-dsh-plugin"`) {
			t.Fatal("personal plugin list route failed", w.Code, w.Body.String())
		}
	})
	t.Run("audit failure rolls back catalog entry", func(t *testing.T) {
		_, err := root.Exec(ctx, `CREATE FUNCTION reject_plugin_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='PLUGIN_ADD' THEN RAISE EXCEPTION 'test audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_plugin_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_plugin_audit()`)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Create(ctx, owner, "rollback-dsh-plugin", "1.0.0", "集成扩展", "rollback"); err == nil {
			t.Fatal("audit failure committed plugin")
		}
		if _, err = s.CreateArchive(ctx, owner, "rollback.tgz", personalPluginArchive(t, "rollback-offline-plugin"), "开发工具", "rollback-offline"); err == nil {
			t.Fatal("audit failure committed encrypted archive")
		}
		_, err = root.Exec(ctx, `DROP TRIGGER reject_plugin_audit ON audit_events`)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		if err = root.QueryRow(ctx, `SELECT count(*) FROM personal_plugins WHERE package_name IN ('rollback-dsh-plugin','rollback-offline-plugin')`).Scan(&count); err != nil || count != 0 {
			t.Fatal("audit failure left plugin row", count, err)
		}
	})
	t.Run("offline HTTP addition encrypts the original package and restricts downloads to its owner", func(t *testing.T) {
		archive := personalPluginArchive(t, "offline-dsh-plugin")
		filename := "离线 插件.tgz"
		body, _ := json.Marshal(map[string]string{"filename": filename, "content_base64": base64.StdEncoding.EncodeToString(archive)})
		saveBody, _ := json.Marshal(map[string]string{"filename": filename, "content_base64": base64.StdEncoding.EncodeToString(archive), "category": "开发工具"})
		server := httpserver.New("api", nil)
		server.WithIdentity(identity.NewHandler(auth))
		server.WithBusiness(httpserver.BusinessMux{Plugins: NewHandler(s, func(context.Context, string, int) error { return nil })}, "https://portal.example")
		request := func(method, path, secret, csrf string, data []byte) *httptest.ResponseRecorder {
			r := httptest.NewRequest(method, path, bytes.NewReader(data)).WithContext(ctx)
			r.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: secret})
			if method == http.MethodPost {
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Origin", "https://portal.example")
				r.Header.Set("X-CSRF-Token", csrf)
			}
			w := httptest.NewRecorder()
			server.ServeHTTP(w, r)
			return w
		}
		calls := len(resolved)
		for _, path := range []string{"/v1/plugins:preview", "/v1/plugins"} {
			if w := request(http.MethodPost, path, ownerSecret, "", saveBody); w.Code != 403 {
				t.Fatal("offline upload bypassed CSRF", w.Code)
			}
		}
		preview := request(http.MethodPost, "/v1/plugins:preview", ownerSecret, owner.CSRFToken, body)
		var metadata Plugin
		if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &metadata) != nil || metadata.ID != "" || metadata.AdditionMethod != "offline" || metadata.Filename != filename || metadata.PackageName != "offline-dsh-plugin" || metadata.SourceNote != "上传包信息及 DSH bundle 声明" {
			t.Fatal("offline preview failed", preview.Code, preview.Body.String())
		}
		var count int
		if err := root.QueryRow(ctx, `SELECT count(*) FROM personal_plugins WHERE package_name='offline-dsh-plugin'`).Scan(&count); err != nil || count != 0 {
			t.Fatal("preview persisted the archive", count, err)
		}
		invalidBody, _ := json.Marshal(map[string]string{"filename": filename, "content_base64": base64.StdEncoding.EncodeToString([]byte("changed invalid package")), "category": "开发工具"})
		if w := request(http.MethodPost, "/v1/plugins", ownerSecret, owner.CSRFToken, invalidBody); w.Code != 422 {
			t.Fatal("save trusted the earlier preview", w.Code, w.Body.String())
		}
		created := request(http.MethodPost, "/v1/plugins", ownerSecret, owner.CSRFToken, saveBody)
		var saved Plugin
		if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &saved) != nil {
			t.Fatal("offline save failed", created.Code, created.Body.String())
		}
		offlineID = saved.ID
		hash := sha256.Sum256(archive)
		if saved.AdditionMethod != "offline" || saved.Filename != filename || saved.SHA256 != hex.EncodeToString(hash[:]) || saved.SHA256 != metadata.SHA256 || saved.Category != "开发工具" || saved.SourceNote != metadata.SourceNote {
			t.Fatal("offline metadata changed", saved)
		}
		var envelope security.Envelope
		if err := root.QueryRow(ctx, `SELECT archive_key_id,archive_nonce,archive_ciphertext FROM personal_plugins WHERE id=$1`, saved.ID).Scan(&envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext); err != nil || bytes.Contains(envelope.Ciphertext, archive) || len(envelope.Nonce) != 12 {
			t.Fatal("archive was not encrypted", err)
		}
		url := "/v1/plugins/" + saved.ID + "/archive"
		download := request(http.MethodGet, url, ownerSecret, "", nil)
		_, disposition, dispositionErr := mime.ParseMediaType(download.Header().Get("Content-Disposition"))
		if download.Code != 200 || !bytes.Equal(download.Body.Bytes(), archive) || download.Header().Get("Content-Type") != "application/gzip" || dispositionErr != nil || disposition["filename"] != filename || download.Header().Get("X-Checksum-SHA256") != saved.SHA256 || download.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("owner could not download the original bytes", download.Code, download.Header())
		}
		for _, path := range []string{url, "/v1/plugins/" + saved.ID + "/archive?owner_id=" + owner.UserID} {
			w := request(http.MethodGet, path, otherSecret, "", nil)
			if w.Code != 404 && w.Code != 422 {
				t.Fatal("another owner downloaded an archive", w.Code)
			}
		}
		if w := request(http.MethodGet, "/v1/plugins/"+saved.ID+"/archive", "", "", nil); w.Code != 401 {
			t.Fatal("unauthenticated download accepted", w.Code)
		}
		if w := request(http.MethodGet, "/v1/plugins/"+saved.ID+"/archive", otherSecret, "", nil); w.Code != 404 {
			t.Fatal("administrator downloaded another owner's archive", w.Code)
		}
		if w := request(http.MethodGet, "/v1/plugins/"+saved.ID+"/archive", ownerSecret, "", nil); w.Code != 200 {
			t.Fatal("archive disappeared after denied request", w.Code)
		}
		if w := request(http.MethodGet, "/v1/plugins/"+initialOnlineID+"/archive", ownerSecret, "", nil); w.Code != 404 {
			t.Fatal("online entry exposed an archive", w.Code)
		}
		if w := request(http.MethodGet, "/v1/plugins/not-a-uuid/archive", ownerSecret, "", nil); w.Code != 404 {
			t.Fatal("malformed ID was not a missing archive", w.Code)
		}
		if w := request(http.MethodPost, "/v1/plugins", ownerSecret, owner.CSRFToken, saveBody); w.Code != 409 {
			t.Fatal("offline duplicate accepted", w.Code)
		}
		listed := request(http.MethodGet, "/v1/plugins", ownerSecret, "", nil)
		if listed.Code != 200 || !strings.Contains(listed.Body.String(), `"addition_method":"offline"`) || strings.Contains(listed.Body.String(), "content_base64") || strings.Contains(listed.Body.String(), "archive_ciphertext") {
			t.Fatal("list exposed archive storage", listed.Code)
		}
		second, err := s.CreateArchive(ctx, owner, "second.tar.gz", personalPluginArchive(t, "second-offline-plugin"), "集成扩展", "aad-test")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = root.Exec(ctx, `UPDATE personal_plugins SET archive_key_id=$2,archive_nonce=$3,archive_ciphertext=$4 WHERE id=$1`, second.ID, envelope.KeyID, envelope.Nonce, envelope.Ciphertext); err != nil {
			t.Fatal(err)
		}
		if _, err = s.DownloadArchive(ctx, owner, second.ID); !pluginProblem(err, "PLUGIN_UNAVAILABLE") {
			t.Fatal("ciphertext could be moved between plugin records", err)
		}
		if len(resolved) != calls {
			t.Fatal("offline path accessed npm registry")
		}
	})
	t.Run("database defaults and metadata edits remain private and optimistic", func(t *testing.T) {
		list, err := s.List(ctx, owner)
		if err != nil {
			t.Fatal(err)
		}
		var template, offline Plugin
		for _, item := range list {
			if !item.Custom && template.ID == "" {
				template = item
			}
			if item.ID == offlineID {
				offline = item
			}
		}
		if template.ID == "" || offline.ID == "" || offline.SourceNote != "上传包信息及 DSH bundle 声明" {
			t.Fatal("missing default or offline fixture")
		}
		server := httpserver.New("api", nil)
		server.WithIdentity(identity.NewHandler(auth))
		server.WithBusiness(httpserver.BusinessMux{Plugins: NewHandler(s, func(context.Context, string, int) error { return nil })}, "https://portal.example")
		request := func(id, secret, origin, csrf string, input any) *httptest.ResponseRecorder {
			body, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest(http.MethodPut, "/v1/plugins/"+id, bytes.NewReader(body)).WithContext(ctx)
			r.AddCookie(&http.Cookie{Name: identity.SessionCookie, Value: secret})
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Origin", origin)
			r.Header.Set("X-CSRF-Token", csrf)
			w := httptest.NewRecorder()
			server.ServeHTTP(w, r)
			return w
		}
		input := editPluginMetadata(template)
		input.Name, input.Author, input.Version = "本人配置的插件", "本人指定发布方", "9.8.7"
		input.Summary, input.Description = "本人摘要", "本人描述\n第二行"
		input.Category, input.Keywords = "开发工具", []string{"自定义", "目录"}
		input.Repository, input.Documentation = "https://example.com/source", "https://example.com/docs"
		input.Compatibility, input.CheckedAt, input.SourceNote = "本人配置版本要求", "2026-11-03", "本人核对说明"
		for _, headers := range [][2]string{{"https://portal.example", ""}, {"https://other.example", owner.CSRFToken}} {
			if w := request(template.ID, ownerSecret, headers[0], headers[1], input); w.Code != 403 || !strings.Contains(w.Body.String(), "CSRF_INVALID") {
				t.Fatal("metadata edit bypassed CSRF", w.Code, w.Body.String())
			}
		}
		w := request(template.ID, ownerSecret, "https://portal.example", owner.CSRFToken, input)
		var edited Plugin
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &edited) != nil || !edited.Custom || edited.ID == template.ID || edited.PackageName != template.PackageName || edited.Revision != template.Revision+1 || edited.SourceNote != input.SourceNote || edited.Name != input.Name || edited.Version != input.Version || edited.Author != input.Author || edited.Description != input.Description || edited.AdditionMethod != "online" {
			t.Fatal("default edit did not persist complete personal metadata", w.Code, w.Body.String())
		}
		for _, principal := range []identity.Principal{owner, other} {
			list, err := s.List(ctx, principal)
			if err != nil {
				t.Fatal(err)
			}
			matches := 0
			for _, item := range list {
				if item.PackageName != template.PackageName {
					continue
				}
				matches++
				if principal.UserID == owner.UserID && (item.ID != edited.ID || item.SourceNote != input.SourceNote) || principal.UserID == other.UserID && (item.ID != template.ID || item.Custom || item.SourceNote != template.SourceNote) {
					t.Fatal("personal override or owner isolation failed", item)
				}
			}
			if matches != 1 {
				t.Fatal("default and override were not deduplicated", matches)
			}
		}
		if w = request(template.ID, ownerSecret, "https://portal.example", owner.CSRFToken, input); w.Code != 409 || !strings.Contains(w.Body.String(), "PLUGIN_VERSION_CONFLICT") {
			t.Fatal("stale default edit overwrote its personal override", w.Code, w.Body.String())
		}
		if w = request(edited.ID, otherSecret, "https://portal.example", other.CSRFToken, editPluginMetadata(edited)); w.Code != 404 {
			t.Fatal("administrator edited another owner's metadata", w.Code, w.Body.String())
		}
		stale := editPluginMetadata(edited)
		stale.ExpectedRevision--
		if w = request(edited.ID, ownerSecret, "https://portal.example", owner.CSRFToken, stale); w.Code != 409 {
			t.Fatal("stale personal edit overwrote metadata", w.Code)
		}
		errors := make(chan error, 2)
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := s.Edit(ctx, owner, edited.ID, editPluginMetadata(edited), "concurrent-edit")
				errors <- err
			}()
		}
		wg.Wait()
		close(errors)
		success, conflicts := 0, 0
		for err := range errors {
			if err == nil {
				success++
			} else if pluginProblem(err, "PLUGIN_VERSION_CONFLICT") {
				conflicts++
			} else {
				t.Fatal(err)
			}
		}
		if success != 1 || conflicts != 1 {
			t.Fatal("concurrent metadata edit bypassed revision", success, conflicts)
		}
		original, err := s.DownloadArchive(ctx, owner, offline.ID)
		if err != nil {
			t.Fatal(err)
		}
		var priorCipher []byte
		if err = root.QueryRow(ctx, `SELECT archive_ciphertext FROM personal_plugins WHERE id=$1`, offline.ID).Scan(&priorCipher); err != nil {
			t.Fatal(err)
		}
		offlineInput := editPluginMetadata(offline)
		offlineInput.Name, offlineInput.Version, offlineInput.SourceNote = "离线资料修改", "2.0.0", ""
		w = request(offline.ID, ownerSecret, "https://portal.example", owner.CSRFToken, offlineInput)
		var offlineEdited Plugin
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &offlineEdited) != nil || offlineEdited.ID != offline.ID || offlineEdited.Filename != offline.Filename || offlineEdited.SHA256 != offline.SHA256 || offlineEdited.AdditionMethod != "offline" || offlineEdited.Revision != offline.Revision+1 {
			t.Fatal("offline edit changed artifact identity", w.Code, w.Body.String())
		}
		after, err := s.DownloadArchive(ctx, owner, offline.ID)
		if err != nil || !bytes.Equal(original.Data, after.Data) {
			t.Fatal("metadata edit changed original archive", err)
		}
		var afterCipher []byte
		if err = root.QueryRow(ctx, `SELECT archive_ciphertext FROM personal_plugins WHERE id=$1`, offline.ID).Scan(&afterCipher); err != nil || !bytes.Equal(priorCipher, afterCipher) {
			t.Fatal("metadata edit rewrote ciphertext", err)
		}
		onlineEdited, err := s.Edit(ctx, owner, initialOnlineID, editPluginMetadata(saved), "online-edit")
		if err != nil || onlineEdited.ID != initialOnlineID || onlineEdited.Revision != 2 {
			t.Fatal("existing online metadata edit failed", onlineEdited, err)
		}
		if _, err = root.Exec(ctx, `CREATE FUNCTION reject_plugin_update_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='PLUGIN_UPDATE' THEN RAISE EXCEPTION 'test audit failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER reject_plugin_update_audit BEFORE INSERT ON audit_events FOR EACH ROW EXECUTE FUNCTION reject_plugin_update_audit()`); err != nil {
			t.Fatal(err)
		}
		rollbackInput := editPluginMetadata(onlineEdited)
		rollbackInput.Name = "must roll back"
		_, editErr := s.Edit(ctx, owner, onlineEdited.ID, rollbackInput, "rollback-edit")
		var rollbackDefault Plugin
		for _, item := range list {
			if !item.Custom && item.ID != template.ID {
				rollbackDefault = item
				break
			}
		}
		_, defaultEditErr := s.Edit(ctx, owner, rollbackDefault.ID, editPluginMetadata(rollbackDefault), "rollback-default-edit")
		if _, err = root.Exec(ctx, `DROP TRIGGER reject_plugin_update_audit ON audit_events; DROP FUNCTION reject_plugin_update_audit()`); err != nil {
			t.Fatal(err)
		}
		var name string
		var revision int64
		if err = root.QueryRow(ctx, `SELECT name,revision FROM personal_plugins WHERE id=$1`, onlineEdited.ID).Scan(&name, &revision); editErr == nil || err != nil || name != onlineEdited.Name || revision != onlineEdited.Revision {
			t.Fatal("audit failure left metadata changes", name, revision, editErr, err)
		}
		var overrides int
		if err = root.QueryRow(ctx, `SELECT count(*) FROM personal_plugins WHERE org_id=$1 AND owner_id=$2 AND default_id=$3`, org, owner.UserID, rollbackDefault.ID).Scan(&overrides); defaultEditErr == nil || err != nil || overrides != 0 {
			t.Fatal("audit failure left a default override", overrides, defaultEditErr, err)
		}
		var protected bool
		if err = root.QueryRow(ctx, `SELECT has_table_privilege('hc_api','plugin_catalog_defaults','SELECT')
 AND NOT has_table_privilege('hc_api','plugin_catalog_defaults','UPDATE')
 AND has_column_privilege('hc_api','personal_plugins','name','UPDATE')
 AND NOT has_column_privilege('hc_api','personal_plugins','owner_id','UPDATE')
 AND NOT has_column_privilege('hc_api','personal_plugins','default_id','UPDATE')
 AND NOT has_column_privilege('hc_api','personal_plugins','archive_ciphertext','UPDATE')`).Scan(&protected); err != nil || !protected {
			t.Fatal("plugin runtime grants widened immutable identity", protected, err)
		}
		var catalogID string
		if err = root.QueryRow(ctx, `INSERT INTO plugin_catalog_defaults(id,name,package_name,package_version,author,category,summary,description,keywords,repository,documentation,compatibility,checked_at,source_note,sort_order)
 SELECT gen_random_uuid(),'DB maintained entry','db-only-plugin','1.0.0',author,category,summary,description,keywords,repository,documentation,compatibility,checked_at,'DB note',100 FROM plugin_catalog_defaults LIMIT 1 RETURNING id::text`).Scan(&catalogID); err != nil {
			t.Fatal(err)
		}
		list, err = s.List(ctx, owner)
		found := false
		for _, item := range list {
			if item.ID == catalogID && item.Name == "DB maintained entry" && !item.Custom && item.SourceNote == "DB note" {
				found = true
			}
		}
		if err != nil || !found {
			t.Fatal("catalog still depended on runtime defaults", err)
		}
		if _, err = s.Create(ctx, owner, "db-only-plugin", "1.0.0", "开发工具", "db-duplicate"); !pluginProblem(err, "PLUGIN_EXISTS") {
			t.Fatal("DB catalog duplicate was not rejected", err)
		}
		if _, err = s.Preview(ctx, owner, "db-only-plugin"); !pluginProblem(err, "PLUGIN_EXISTS") {
			t.Fatal("DB catalog duplicate preview was not rejected", err)
		}
	})
	t.Run("concurrent add stays unique and respects the limit", func(t *testing.T) {
		concurrent := NewService(app, cipher, "test", false).WithRegistry(resolveFunc(func(_ context.Context, spec string) (Plugin, error) { return metadata(spec), nil }))
		var wg sync.WaitGroup
		errors := make(chan error, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := concurrent.Create(ctx, other, "concurrent-dsh-plugin", "1.0.0", "开发工具", "concurrent")
				errors <- err
			}()
		}
		wg.Wait()
		close(errors)
		success, duplicates := 0, 0
		for err := range errors {
			if err == nil {
				success++
			} else if pluginProblem(err, "PLUGIN_EXISTS") {
				duplicates++
			} else {
				t.Fatal(err)
			}
		}
		if success != 1 || duplicates != 1 {
			t.Fatal("concurrent add was not unique", success, duplicates)
		}
		_, err := root.Exec(ctx, `INSERT INTO personal_plugins(id,org_id,owner_id,name,package_name,package_version,author,category,summary,description,compatibility,checked_at)
 SELECT gen_random_uuid(),$1,$2,'limit fixture','limit-plugin-'||n,'1.0.0','fixture','集成扩展','','','unknown',current_date FROM generate_series(1,98) n`, org, other.UserID)
		if err != nil {
			t.Fatal(err)
		}
		limitErrors := make(chan error, 2)
		for _, name := range []string{"last-slot-one", "last-slot-two"} {
			wg.Add(1)
			go func(name string) {
				defer wg.Done()
				_, err := concurrent.Create(ctx, other, name, "1.0.0", "开发工具", "last-slot")
				limitErrors <- err
			}(name)
		}
		wg.Wait()
		close(limitErrors)
		success, limited := 0, 0
		for err := range limitErrors {
			if err == nil {
				success++
			} else if pluginProblem(err, "PLUGIN_LIMIT") {
				limited++
			} else {
				t.Fatal(err)
			}
		}
		if success != 1 || limited != 1 {
			t.Fatal("concurrent add exceeded 100-entry limit", success, limited)
		}
		if _, err = concurrent.Create(ctx, other, "over-limit-plugin", "1.0.0", "开发工具", "limit"); !pluginProblem(err, "PLUGIN_LIMIT") {
			t.Fatal("plugin limit bypassed", err)
		}
		list, err := concurrent.List(ctx, other)
		if err != nil {
			t.Fatal(err)
		}
		var template Plugin
		for _, item := range list {
			if !item.Custom {
				template = item
				break
			}
		}
		if template.ID == "" {
			t.Fatal("missing default at personal quota")
		}
		if _, err = concurrent.Edit(ctx, other, template.ID, editPluginMetadata(template), "default-at-limit"); err != nil {
			t.Fatal("personal add quota blocked default metadata edit", err)
		}
		var additions, overrides int
		if err = root.QueryRow(ctx, `SELECT count(*) FILTER(WHERE default_id IS NULL),count(*) FILTER(WHERE default_id IS NOT NULL) FROM personal_plugins WHERE org_id=$1 AND owner_id=$2`, org, other.UserID).Scan(&additions, &overrides); err != nil || additions != 100 || overrides != 1 {
			t.Fatal("default override consumed personal addition quota", additions, overrides, err)
		}
	})
	t.Run("current authority and maintenance apply before outbound calls", func(t *testing.T) {
		calls := len(resolved)
		if _, err = NewService(app, cipher, "test", true).WithRegistry(s.registry).Create(ctx, owner, "maintenance-plugin", "1.0.0", "开发工具", "maintenance"); !pluginProblem(err, "MAINTENANCE") {
			t.Fatal("maintenance allowed add", err)
		}
		if _, err = NewService(app, cipher, "test", true).Edit(ctx, owner, initialOnlineID, editPluginMetadata(saved), "maintenance-edit"); !pluginProblem(err, "MAINTENANCE") {
			t.Fatal("maintenance allowed metadata edit", err)
		}
		if _, err = root.Exec(ctx, `DELETE FROM role_permissions WHERE role_id=$1 AND permission_id='plugins:manage'`, owner.RoleID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.Preview(ctx, owner, "denied-plugin"); !errors.Is(err, identity.ErrForbidden) {
			t.Fatal("stale permission allowed registry call", err)
		}
		if _, err = s.Edit(ctx, owner, initialOnlineID, editPluginMetadata(saved), "denied-edit"); !errors.Is(err, identity.ErrForbidden) {
			t.Fatal("stale permission allowed metadata edit", err)
		}
		if len(resolved) != calls {
			t.Fatal("unauthorized write reached registry")
		}
		if _, err = s.List(ctx, owner); err != nil {
			t.Fatal("read requires plugin manage permission", err)
		}
		if _, err = s.DownloadArchive(ctx, owner, offlineID); err != nil {
			t.Fatal("download required plugin write permission", err)
		}
		if _, err = root.Exec(ctx, `UPDATE memberships SET auth_generation=auth_generation+1 WHERE org_id=$1 AND user_id=$2`, org, owner.UserID); err != nil {
			t.Fatal(err)
		}
		if _, err = s.List(ctx, owner); !errors.Is(err, identity.ErrUnauthenticated) {
			t.Fatal("revoked session read plugins", err)
		}
	})
}

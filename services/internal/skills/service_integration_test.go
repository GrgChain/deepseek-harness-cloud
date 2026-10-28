package skills

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jonluo94/deepseek-harness-cloud/db"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/devlocal"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type testProvider struct{ subject, state, nonce string }

func (p *testProvider) Authorize(state, nonce, verifier string) string {
	p.state = state
	p.nonce = nonce
	return "https://idp.example/auth"
}
func (p *testProvider) Exchange(context.Context, string, string) (identity.VerifiedIdentity, error) {
	return identity.VerifiedIdentity{Issuer: "https://idp.example", Subject: p.subject, Nonce: p.nonce}, nil
}
func problemIs(err error, code string) bool {
	var p *webapi.Problem
	return errors.As(err, &p) && p.Code == code
}

func TestPersonalLibraryWithPostgres(t *testing.T) {
	filename := os.Getenv("HC_TEST_DB_DSN_FILE")
	if filename == "" {
		t.Skip("isolated PostgreSQL required")
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal("cannot read connection file")
	}
	u, err := url.Parse(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal("invalid connection file")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	u.Path = "/postgres"
	admin, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatal("database administrator unavailable")
	}
	database := "hc_test_skills_" + strings.ReplaceAll(webapi.ID(), "-", "")
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{database}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	var root, app *pgxpool.Pool
	defer func() {
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
	}()
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
	config.AfterConnect = func(ctx context.Context, c *pgx.Conn) error { _, e := c.Exec(ctx, "SET ROLE hc_api"); return e }
	app, err = pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	org := webapi.ID()
	if err = identity.ApplyMapping(ctx, root, identity.Mapping{SchemaVersion: 1, OrganizationID: org, Members: []identity.MemberMapping{{Issuer: "https://idp.example", Subject: "owner", Role: "MEMBER", WorkspaceOwnerID: webapi.ID()}, {Issuer: "https://idp.example", Subject: "admin", Role: "ADMIN", WorkspaceOwnerID: webapi.ID()}}}, "skills-test"); err != nil {
		t.Fatal(err)
	}
	cipher, _ := security.NewSecretCipher("test-key", bytes.Repeat([]byte{7}, 32))
	s := NewService(app, cipher, "test", false)
	login := func(subject string) identity.Principal {
		t.Helper()
		provider := &testProvider{subject: subject}
		auth := identity.NewService(app, provider, cipher, "test", org, "https://portal.example")
		l, e := auth.Login(ctx, "", "/app/skills")
		if e != nil {
			t.Fatal(e)
		}
		result, e := auth.Callback(ctx, provider.state, l.BrowserSecret, provider.state, "")
		if e != nil {
			t.Fatal(e)
		}
		p, e := auth.Authenticate(ctx, result.SessionSecret)
		if e != nil {
			t.Fatal(e)
		}
		return p
	}
	owner, other := login("owner"), login("admin")
	skill, err := s.Create(ctx, owner, "notes", "整理个人会议记录", "")
	if err != nil {
		t.Fatal(err)
	}
	if !owner.Has("skills:manage") {
		t.Fatal("fresh member lacks permissions")
	}
	if _, err = s.Get(ctx, other, skill.ID); !problemIs(err, "NOT_FOUND") {
		t.Fatal("admin read owner's skill", err)
	}
	if _, _, err = s.Export(ctx, other, []string{skill.ID}, false); !problemIs(err, "NOT_FOUND") {
		t.Fatal("cross-owner export", err)
	}
	skill, err = s.Get(ctx, owner, skill.ID)
	if err != nil {
		t.Fatal(err)
	}
	edited, err := s.Edit(ctx, owner, skill.ID, skill.Version, skill.Content+"\nprivate body token\n", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Edit(ctx, owner, skill.ID, skill.Version, "stale", nil, ""); !problemIs(err, "VERSION_CONFLICT") {
		t.Fatal("accepted stale edit", err)
	}
	var ciphertext []byte
	if err = root.QueryRow(ctx, "SELECT ciphertext FROM personal_skills WHERE id=$1", skill.ID).Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte("private body token")) {
		t.Fatal("stored plaintext")
	}
	preview, err := s.Preview(ctx, owner, "notes.md", markdown("notes", "remote body"), "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(ctx, other, preview.Token, "replace", ""); !problemIs(err, "NOT_FOUND") {
		t.Fatal("cross-owner preview apply", err)
	}
	result, err := s.Apply(ctx, owner, preview.Token, "replace", "")
	if err != nil || len(result["applied"]) != 1 {
		t.Fatal(result, err)
	}
	current, err := s.Get(ctx, owner, skill.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.ModelInvocable || current.UserInvocable || !current.HasPrevious {
		t.Fatal("import enabled or lost backup")
	}
	old, _, err := s.Export(ctx, owner, []string{skill.ID}, true)
	if err != nil || !strings.Contains(string(old), "private body token") {
		t.Fatal("previous package lost", err)
	}
	if _, err = s.Apply(ctx, owner, preview.Token, "replace", ""); !problemIs(err, "NOT_FOUND") {
		t.Fatal("replayed preview", err)
	}
	// Delete and recreate a same-name, same-version resource after preview (ABA).
	aba, err := s.Create(ctx, owner, "aba", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	stale, err := s.Preview(ctx, owner, "aba.md", markdown("aba", "new"), "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Delete(ctx, owner, aba.ID, aba.Version, "aba", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(ctx, owner, "aba", "recreated", ""); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(ctx, owner, stale.Token, "replace", ""); !problemIs(err, "VERSION_CONFLICT") {
		t.Fatal("ABA overwrite", err)
	}
	// Manual source refresh keeps current flags and never writes during preview/failure.
	source := "https://example.com/source.md"
	remote := markdown("source", "remote one")
	s.fetch = func(context.Context, string) ([]byte, string, error) { return remote, source, nil }
	fetched, err := s.Preview(ctx, owner, "", nil, source, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(ctx, owner, fetched.Token, "replace", ""); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, owner, "source", 1, 10)
	if err != nil || len(list.Items) != 1 {
		t.Fatal(list, err)
	}
	linked := list.Items[0]
	enabled, err := s.Edit(ctx, owner, linked.ID, linked.Version, "", &[2]bool{true, true}, "")
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.CheckUpdate(ctx, owner, enabled.ID, enabled.Version)
	if err != nil || !unchanged.Unchanged || unchanged.Token != "" {
		t.Fatal("policy-only change detected as source change", err)
	}
	remote = markdown("source", "remote two")
	update, err := s.CheckUpdate(ctx, owner, enabled.ID, enabled.Version)
	if err != nil || len(update.Items[0].ChangedFiles) == 0 {
		t.Fatal(update, err)
	}
	if _, err = s.Apply(ctx, owner, update.Token, "replace", ""); err != nil {
		t.Fatal(err)
	}
	linked, err = s.Get(ctx, owner, enabled.ID)
	if err != nil || !linked.ModelInvocable || !linked.UserInvocable || !strings.Contains(linked.Content, "remote two") {
		t.Fatal("sync lost policy", err)
	}
	s.fetch = func(context.Context, string) ([]byte, string, error) { return nil, "", errors.New("remote offline") }
	if _, err = s.CheckUpdate(ctx, owner, linked.ID, linked.Version); err == nil {
		t.Fatal("expected source failure")
	}
	after, err := s.Get(ctx, owner, linked.ID)
	if err != nil || after.Version != linked.Version || after.Content != linked.Content {
		t.Fatal("failed sync changed source", err)
	}
	// Expired staging data cannot be consumed.
	expiry, err := s.Preview(ctx, owner, "expire.md", markdown("expire", "body"), "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = root.Exec(ctx, "UPDATE skill_import_previews SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1", expiry.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(ctx, owner, expiry.Token, "replace", ""); !problemIs(err, "NOT_FOUND") {
		t.Fatal("expired import accepted", err)
	}
	// A repository import can contain several skills; refreshing one must
	// preserve every other record and never create newly added neighbours.
	repoURL := "https://github.com/team/repo"
	first := Bundle{Files: map[string][]byte{"SKILL.md": markdown("repo-first", "original")}}
	second := Bundle{Files: map[string][]byte{"SKILL.md": markdown("repo-second", "neighbour")}}
	repoData, _, err := exportBundles([]Bundle{first, second})
	if err != nil {
		t.Fatal(err)
	}
	s.fetch = func(context.Context, string) ([]byte, string, error) { return repoData, repoURL, nil }
	repoPreview, err := s.Preview(ctx, owner, "", nil, repoURL, "", 0)
	if err != nil || len(repoPreview.Items) != 2 {
		t.Fatal("repository preview missing skills", err)
	}
	if _, err = s.Apply(ctx, owner, repoPreview.Token, "skip", ""); err != nil {
		t.Fatal(err)
	}
	repoList, err := s.List(ctx, owner, "repo-", 1, 10)
	if err != nil || len(repoList.Items) != 2 {
		t.Fatal("repository import missing", err)
	}
	var target, neighbour Skill
	for _, item := range repoList.Items {
		if item.Name == "repo-first" {
			target = item
		} else {
			neighbour = item
		}
	}
	first.Files["SKILL.md"] = markdown("repo-first", "updated")
	third := Bundle{Files: map[string][]byte{"SKILL.md": markdown("repo-third", "not imported")}}
	repoData, _, err = exportBundles([]Bundle{first, second, third})
	if err != nil {
		t.Fatal(err)
	}
	repoUpdate, err := s.CheckUpdate(ctx, owner, target.ID, target.Version)
	if err != nil || len(repoUpdate.Items) != 1 || repoUpdate.Items[0].Name != target.Name {
		t.Fatal("repository sync staged unrelated skills", err)
	}
	if _, err = s.Apply(ctx, owner, repoUpdate.Token, "replace", ""); err != nil {
		t.Fatal(err)
	}
	repoList, err = s.List(ctx, owner, "repo-", 1, 10)
	if err != nil || len(repoList.Items) != 2 {
		t.Fatal("repository sync created neighbour", err)
	}
	kept, err := s.Get(ctx, owner, neighbour.ID)
	if err != nil || kept.Version != neighbour.Version || !strings.Contains(kept.Content, "neighbour") {
		t.Fatal("repository sync changed neighbour", err)
	}
	target, err = s.Get(ctx, owner, target.ID)
	if err != nil || !strings.Contains(target.Content, "updated") {
		t.Fatal("repository sync did not update target", err)
	}
	repoData, _, err = exportBundles([]Bundle{second, third})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CheckUpdate(ctx, owner, target.ID, target.Version); !problemIs(err, "SKILL_INVALID") {
		t.Fatal("missing remote skill was replaced", err)
	}
	kept, err = s.Get(ctx, owner, target.ID)
	if err != nil || kept.Version != target.Version || kept.Content != target.Content {
		t.Fatal("missing remote skill changed local record", err)
	}
	if source := os.Getenv("HC_REAL_SKILL_SOURCE_URL"); source != "" {
		// Opt-in public HTTPS download uses the production DNS/TLS transport and
		// only this disposable database. External skill instructions are data.
		s.fetch = fetchSource
		preview, err := s.Preview(ctx, owner, "", nil, source, "", 0)
		if err != nil || len(preview.Items) == 0 {
			var problem *webapi.Problem
			if errors.As(err, &problem) {
				t.Fatal("live source preview failed:", problem.Message)
			}
			t.Fatal("live source preview failed", err)
		}
		t.Logf("live source discovered %d skills", len(preview.Items))
		name := preview.Items[0].Name
		if _, err = s.Apply(ctx, owner, preview.Token, "skip", ""); err != nil {
			t.Fatal("live source import failed", err)
		}
		list, err := s.List(ctx, owner, name, 1, 10)
		if err != nil || len(list.Items) != 1 {
			t.Fatal("live import missing", err)
		}
		imported, err := s.Get(ctx, owner, list.Items[0].ID)
		if err != nil || imported.ModelInvocable || imported.UserInvocable || imported.SourceURL == "" {
			t.Fatal("live source lost origin or enabled invocation", err)
		}
		unchanged, err := s.CheckUpdate(ctx, owner, imported.ID, imported.Version)
		if err != nil || !unchanged.Unchanged || unchanged.Token != "" {
			var problem *webapi.Problem
			if errors.As(err, &problem) {
				t.Fatal("live source update failed:", problem.Message)
			}
			t.Fatal("unchanged live source requires replacement", err)
		}
		exported, filename, err := s.Export(ctx, owner, []string{imported.ID}, false)
		if err != nil {
			t.Fatal("live source export failed", err)
		}
		bundles, err := parseImport(filename, exported)
		if err != nil || len(bundles) != 1 || string(bundles[0].Files["SKILL.md"]) != imported.Content || len(bundles[0].Files) != imported.FileCount {
			t.Fatal("live source export differs from saved package", err)
		}
	}
	testSkillResourceFiles(t, ctx, s, owner, other)
	testLargeSkillImport(t, ctx, s, owner)
	// Changing permissions takes effect even with an old presentation hint.
	if _, err = root.Exec(ctx, "DELETE FROM role_permissions WHERE role_id=$1 AND permission_id='skills:manage'", owner.RoleID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Edit(ctx, owner, current.ID, current.Version, "", &[2]bool{true, true}, ""); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("stale permission authorized", err)
	}
	if _, err = s.SearchHub(ctx, owner, "skills.sh", "browser"); !errors.Is(err, identity.ErrForbidden) {
		t.Fatal("stale permission authorized a public hub query", err)
	}
	_ = edited
}

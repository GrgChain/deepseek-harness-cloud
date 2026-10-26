package skills

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
)

func testSkillResourceFiles(t *testing.T, ctx context.Context, s *Service, owner, other identity.Principal) {
	t.Helper()
	bundle := Bundle{Files: map[string][]byte{
		"SKILL.md":             markdown("editable-resources", "Root instructions stay intact."),
		"references/guide.txt": []byte("附属说明\n"),
		"scripts/run.py":       []byte("print('example')\n"),
		"assets/chart.png":     {137, 80, 78, 71, 13, 10, 26, 10, 0},
		"references/empty.txt": {},
	}}
	remote, _, err := exportBundles([]Bundle{bundle})
	if err != nil {
		t.Fatal(err)
	}
	previousFetcher := s.fetch
	defer func() { s.fetch = previousFetcher }()
	source := "https://example.com/resources.zip"
	s.fetch = func(context.Context, string) ([]byte, string, error) { return remote, source, nil }
	preview, err := s.Preview(ctx, owner, "", nil, source, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Apply(ctx, owner, preview.Token, "skip", ""); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, owner, "editable-resources", 1, 10)
	if err != nil || len(list.Items) != 1 {
		t.Fatal("resource skill missing", err)
	}
	skill, err := s.Get(ctx, owner, list.Items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, filename := range []string{"references/guide.txt", "scripts/run.py", "references/empty.txt"} {
		file, err := s.ReadFile(ctx, owner, skill.ID, filename)
		if err != nil || !file.Editable || file.Content != string(bundle.Files[filename]) || file.Version != skill.Version {
			t.Fatal("resource text read failed", filename, err)
		}
	}
	binary, err := s.ReadFile(ctx, owner, skill.ID, "assets/chart.png")
	if err != nil || binary.Editable || binary.Content != "" {
		t.Fatal("binary exposed as editable text", err)
	}
	if _, err = s.ReadFile(ctx, other, skill.ID, "references/guide.txt"); !problemIs(err, "NOT_FOUND") {
		t.Fatal("cross-owner resource read", err)
	}
	if _, err = s.EditFile(ctx, other, skill.ID, skill.Version, "references/guide.txt", "attack", ""); !problemIs(err, "NOT_FOUND") {
		t.Fatal("cross-owner resource write", err)
	}
	for _, filename := range []string{"../guide.txt", "/tmp/file", "references/../SKILL.md", "references\\guide.txt"} {
		if _, err = s.ReadFile(ctx, owner, skill.ID, filename); !problemIs(err, "SKILL_INVALID") {
			t.Fatal("unsafe resource read", filename, err)
		}
		if _, err = s.EditFile(ctx, owner, skill.ID, skill.Version, filename, "attack", ""); !problemIs(err, "SKILL_INVALID") {
			t.Fatal("unsafe resource write", filename, err)
		}
	}
	if _, err = s.EditFile(ctx, owner, skill.ID, skill.Version, "new.txt", "new", ""); !problemIs(err, "NOT_FOUND") {
		t.Fatal("editing created an unlisted file", err)
	}
	for _, tc := range []struct{ filename, content string }{
		{"assets/chart.png", "overwrite image"}, {"references/guide.txt", "binary\x00"},
		{"references/guide.txt", string([]byte{0xff})}, {"references/guide.txt", strings.Repeat("x", MaxFile+1)},
		{"SKILL.md", string(markdown("renamed", "body"))},
	} {
		if _, err = s.EditFile(ctx, owner, skill.ID, skill.Version, tc.filename, tc.content, ""); !problemIs(err, "SKILL_INVALID") {
			t.Fatal("unsafe resource edit", tc.filename, err)
		}
	}
	updated, err := s.EditFile(ctx, owner, skill.ID, skill.Version, "references/guide.txt", "private attachment marker\n新内容\n", "")
	if err != nil || updated.Version != skill.Version+1 || updated.SourceURL != source {
		t.Fatal("resource save failed", err)
	}
	if _, err = s.EditFile(ctx, owner, skill.ID, skill.Version, "scripts/run.py", "stale", ""); !problemIs(err, "VERSION_CONFLICT") {
		t.Fatal("accepted stale resource version", err)
	}
	exported, filename, err := s.Export(ctx, owner, []string{skill.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parseImport(filename, exported)
	if err != nil || len(parsed) != 1 {
		t.Fatal("resource export failed", err)
	}
	for filename, original := range bundle.Files {
		expected := original
		if filename == "references/guide.txt" {
			expected = []byte("private attachment marker\n新内容\n")
		} else if filename == "SKILL.md" {
			expected = []byte(skill.Content) // Import normalizes portable metadata.
		}
		if !bytes.Equal(parsed[0].Files[filename], expected) {
			t.Fatal("resource edit changed another file", filename)
		}
	}
	refresh, err := s.CheckUpdate(ctx, owner, skill.ID, updated.Version)
	if err != nil || !refresh.Items[0].LocalChanged || len(refresh.Items[0].ChangedFiles) != 1 || refresh.Items[0].ChangedFiles[0] != "references/guide.txt" {
		t.Fatal("resource local changes missing from sync preview", err)
	}
	// Empty text is valid and changes the bundle version, not the file count.
	cleared, err := s.EditFile(ctx, owner, skill.ID, updated.Version, "references/guide.txt", "", "")
	if err != nil || cleared.FileCount != skill.FileCount {
		t.Fatal("could not save an empty text file", err)
	}
	file, err := s.ReadFile(ctx, owner, skill.ID, "references/guide.txt")
	if err != nil || !file.Editable || file.Content != "" {
		t.Fatal("empty text changed into binary", err)
	}
	h := NewHandler(s, func(context.Context, string, int) error { return nil })
	for _, tc := range []struct {
		method, query, body string
		status              int
	}{
		{"GET", "?path=" + url.QueryEscape("scripts/run.py"), "", 200},
		{"GET", "?path=scripts/run.py&path=SKILL.md", "", 422},
		{"GET", "?path=scripts/run.py&extra=true", "", 422},
		{"GET", "", "", 422},
		{"PUT", "", `{"path":"scripts/run.py","content":null,"expected_version":1}`, 422},
		{"PUT", "", `{"path":"scripts/run.py","expected_version":1}`, 422},
		{"PUT", "", `{"path":"scripts/run.py","content":"body","extra":true}`, 422},
	} {
		request := httptest.NewRequest(tc.method, "/v1/skills/"+skill.ID+":file"+tc.query, strings.NewReader(tc.body)).WithContext(ctx)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.ServeAuthenticated(response, request, owner)
		if response.Code != tc.status {
			t.Fatal("file route status", tc.method, tc.query, response.Code, response.Body.String())
		}
		if tc.status == 200 {
			var data FileContent
			if json.Unmarshal(response.Body.Bytes(), &data) != nil || data.Content != string(bundle.Files["scripts/run.py"]) {
				t.Fatal("file route changed content")
			}
		}
	}
	created, err := s.Create(ctx, owner, "new-skill-scripts", "新建技能添加脚本", "")
	if err != nil {
		t.Fatal(err)
	}
	rootBefore, err := s.ReadFile(ctx, owner, created.ID, "SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateFile(ctx, other, created.ID, created.Version, "scripts/run.py", "print('test')", ""); !problemIs(err, "NOT_FOUND") {
		t.Fatal("cross-owner file creation", err)
	}
	for _, filename := range []string{"../run.py", "/tmp/run.py", ".env", "scripts/../run.py", "SKILL.md", "skill.MD", "SKILL.md/run.py"} {
		if _, err = s.CreateFile(ctx, owner, created.ID, created.Version, filename, "example", ""); !problemIs(err, "SKILL_INVALID") {
			t.Fatal("unsafe or conflicting file creation", filename, err)
		}
	}
	for _, content := range []string{"binary\x00", string([]byte{0xff}), strings.Repeat("x", MaxFile+1)} {
		if _, err = s.CreateFile(ctx, owner, created.ID, created.Version, "scripts/run.py", content, ""); !problemIs(err, "SKILL_INVALID") {
			t.Fatal("invalid file content accepted", err)
		}
	}
	script := "#!/usr/bin/env python3\nprint('中文脚本')\n"
	withScript, err := s.CreateFile(ctx, owner, created.ID, created.Version, "scripts/run.py", script, "")
	if err != nil || withScript.Version != created.Version+1 || withScript.FileCount != 2 {
		t.Fatal("new skill script creation failed", err)
	}
	if _, err = s.CreateFile(ctx, owner, created.ID, created.Version, "scripts/second.sh", "echo test", ""); !problemIs(err, "VERSION_CONFLICT") {
		t.Fatal("stale create accepted", err)
	}
	for _, filename := range []string{"scripts/RUN.py", "scripts", "scripts/run.py/child.txt"} {
		if _, err = s.CreateFile(ctx, owner, created.ID, withScript.Version, filename, "overwrite", ""); !problemIs(err, "SKILL_INVALID") {
			t.Fatal("conflicting script creation accepted", filename, err)
		}
	}
	requestBody, _ := json.Marshal(map[string]any{"path": "references/empty.txt", "content": "", "expected_version": withScript.Version})
	request := httptest.NewRequest("POST", "/v1/skills/"+created.ID+":file", bytes.NewReader(requestBody)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.ServeAuthenticated(response, request, owner)
	if response.Code != 200 {
		t.Fatal("create file HTTP route failed", response.Code, response.Body.String())
	}
	archive, archiveName, err := s.Export(ctx, owner, []string{created.ID}, false)
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := parseImport(archiveName, archive)
	if err != nil || len(bundles) != 1 || len(bundles[0].Files) != 3 || string(bundles[0].Files["scripts/run.py"]) != script || string(bundles[0].Files["SKILL.md"]) != rootBefore.Content {
		t.Fatal("new script export changed files", err)
	}
}

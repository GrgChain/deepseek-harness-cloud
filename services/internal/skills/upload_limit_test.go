package skills

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
)

func TestLargeSkillImportAndExport(t *testing.T) {
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	add := func(name string, content []byte) {
		t.Helper()
		entry, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	add("SKILL.md", markdown("large-skill", "large skill"))
	add("assets/large.bin", bytes.Repeat([]byte("x"), 13<<20))
	for i := 0; i < 150; i++ {
		add(fmt.Sprintf("assets/file-%d.txt", i), []byte("data"))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if archive.Len() <= MaxUpload {
		t.Fatal("archive must exceed old upload limit")
	}
	body, err := json.Marshal(map[string]string{"filename": "large.zip", "content_base64": base64.StdEncoding.EncodeToString(archive.Bytes())})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/v1/skills:preview", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	var input struct {
		Filename string `json:"filename"`
		Content  string `json:"content_base64"`
	}
	if err = decode(httptest.NewRecorder(), request, &input); err != nil {
		t.Fatal("large request rejected", err)
	}
	data, err := base64.StdEncoding.Strict().DecodeString(input.Content)
	if err != nil {
		t.Fatal(err)
	}
	bundles, err := parseImport(input.Filename, data)
	if err != nil || len(bundles) != 1 || len(bundles[0].Files) != 152 || len(bundles[0].Files["assets/large.bin"]) != 13<<20 {
		t.Fatal("large bundle rejected", err)
	}
	exported, _, err := exportBundles(bundles)
	if err != nil {
		t.Fatal("large bundle export rejected", err)
	}
	roundtrip, err := parseImport("export.zip", exported)
	if err != nil || len(roundtrip) != 1 || len(roundtrip[0].Files) != 152 {
		t.Fatal("large bundle round trip failed", err)
	}
}

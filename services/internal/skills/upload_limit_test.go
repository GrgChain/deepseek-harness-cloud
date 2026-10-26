package skills

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFiveMiBUploadBoundary(t *testing.T) {
	// A valid archive at the limit, with each resource below its own 1 MiB
	// limit, exercises the actual ZIP parser rather than only a byte counter.
	archive := func(lastSize int) []byte {
		var out bytes.Buffer
		w := zip.NewWriter(&out)
		entry, err := w.CreateHeader(&zip.FileHeader{Name: "SKILL.md", Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		entry.Write(markdown("upload-limit", "upload boundary"))
		for i := 0; i < 5; i++ {
			entry, err = w.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("assets/file-%d.bin", i), Method: zip.Store})
			if err != nil {
				t.Fatal(err)
			}
			size := MaxFile
			if i == 4 {
				size = lastSize
			}
			entry.Write(make([]byte, size))
		}
		if err = w.Close(); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	lastSize := MaxFile - 4096
	initial := archive(lastSize)
	data := archive(lastSize + (5 << 20) - len(initial))
	if len(data) != 5<<20 {
		t.Fatal("fixture must be exactly 5 MiB")
	}
	if bundles, err := parseImport("skill.zip", data); err != nil || len(bundles) != 1 || len(bundles[0].Files) != 6 {
		t.Fatal("valid 5 MiB upload rejected", err)
	}
	if _, err := parseImport("skill.zip", append(data, 0)); !problemIs(err, "SKILL_INVALID") {
		t.Fatal("upload over 5 MiB accepted", err)
	}
	// JSON transports the archive as base64, which is larger than 6 MiB.
	body, err := json.Marshal(map[string]string{"filename": "skill.zip", "content_base64": base64.StdEncoding.EncodeToString(data)})
	if err != nil || len(body) <= 6<<20 {
		t.Fatal("invalid base64 fixture", err)
	}
	request := httptest.NewRequest("POST", "/v1/skills:preview", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	var input struct {
		Filename string `json:"filename"`
		Content  string `json:"content_base64"`
	}
	if err = decode(httptest.NewRecorder(), request, &input); err != nil {
		t.Fatal("base64 envelope for 5 MiB upload rejected", err)
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(input.Content)
	if err != nil || !bytes.Equal(decoded, data) {
		t.Fatal("upload content changed", err)
	}
	request = httptest.NewRequest("POST", "/v1/skills:preview", strings.NewReader(strings.Repeat("x", (7<<20)+1)))
	request.Header.Set("Content-Type", "application/json")
	if err = decode(httptest.NewRecorder(), request, &input); !problemIs(err, "PAYLOAD_TOO_LARGE") {
		t.Fatal("request body limit not enforced", err)
	}
}

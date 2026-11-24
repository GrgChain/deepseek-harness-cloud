package skills

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestParseImportRejectsExcessiveEntryCount(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := range 2005 {
		w, err := zw.Create(fmt.Sprintf("files/f-%04d.txt", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := parseImport("many.zip", buf.Bytes())
	var fail *webapi.Problem
	if !errors.As(err, &fail) || fail.Status != 413 || fail.Code != "PAYLOAD_TOO_LARGE" {
		t.Fatalf("expected 413 PAYLOAD_TOO_LARGE, got %v", err)
	}
}

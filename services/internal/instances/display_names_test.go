package instances

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

func TestDisplayNamesSupportChineseAndCountUnicodeCharacters(t *testing.T) {
	for _, name := range []string{"研发", "研发工作空间_01", "DSH-测试", "1", strings.Repeat("研", 32), strings.Repeat("𠮷", 32)} {
		if !(CreateInput{Name: &name}).valid() || !(CommandInput{ConfirmedName: &name}).valid(Delete) {
			t.Fatalf("valid display name rejected: %q", name)
		}
	}
	for _, name := range []string{"", strings.Repeat("研", 33), "研发/测试", "研发\n测试", "研发\u200b测试", " 研发", "研发 ", "研发%20", "研发😀"} {
		if (CreateInput{Name: &name}).valid() || (CommandInput{ConfirmedName: &name}).valid(Delete) {
			t.Fatalf("invalid display name accepted: %q", name)
		}
	}
}

func TestDeletionDecodesChineseNameExactlyOnce(t *testing.T) {
	for _, name := range []string{"研发工作空间", "DSH-one", "1"} {
		f := &commandHTTPFake{}
		r := httptest.NewRequest("DELETE", "/v1/instances/"+webapi.ID(), nil)
		r.Header.Set("Idempotency-Key", "display-name-test-01")
		r.Header.Set("X-Confirm-Instance-Name", url.PathEscape(name))
		w := httptest.NewRecorder()
		NewCommandHandler(f, func(context.Context, string, int) error { return nil }).ServeAuthenticated(w, r, identity.Principal{})
		if w.Code != 202 || f.calls != 1 || f.input.ConfirmedName == nil || *f.input.ConfirmedName != name {
			t.Fatalf("encoded confirmation failed: %d", w.Code)
		}
	}
	for _, name := range []string{"%zz", "%E7", "%252F", "%00", "%0A"} {
		f := &commandHTTPFake{}
		r := httptest.NewRequest("DELETE", "/v1/instances/"+webapi.ID(), nil)
		r.Header.Set("Idempotency-Key", "display-name-test-01")
		r.Header.Set("X-Confirm-Instance-Name", name)
		w := httptest.NewRecorder()
		NewCommandHandler(f, nil).ServeAuthenticated(w, r, identity.Principal{})
		if w.Code != 422 || f.calls != 0 {
			t.Fatal("invalid encoded confirmation accepted")
		}
	}
}

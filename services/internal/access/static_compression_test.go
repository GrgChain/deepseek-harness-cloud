package access

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticCompressionPreservesCodeAndNegotiation(t *testing.T) {
	code := strings.Repeat("window.example = 'workspace';\n", 1000)
	for _, value := range []string{"gzip", "br, gzip;q=0.8", "", "gzip;q=0", "gzip;q=0, *;q=1", "gzip;q=bad", "gzip;q=NaN"} {
		t.Run(value, func(t *testing.T) {
			response := &http.Response{StatusCode: 200, Request: httptest.NewRequest("GET", "https://example.test/plugins/?packages=code", nil), Header: http.Header{"Content-Type": {"application/javascript"}, "Content-Length": {"29000"}, "ETag": {"original"}}, Body: io.NopCloser(strings.NewReader(code)), ContentLength: int64(len(code))}
			compressStaticResponse(response, value)
			defer response.Body.Close()
			var reader io.Reader = response.Body
			if value == "gzip" || value == "br, gzip;q=0.8" {
				if response.Header.Get("Content-Encoding") != "gzip" || response.Header.Get("Content-Length") != "" || response.Header.Get("ETag") != "" || response.ContentLength != -1 {
					t.Fatal("incorrect compressed metadata")
				}
				zip, err := gzip.NewReader(reader)
				if err != nil {
					t.Fatal(err)
				}
				defer zip.Close()
				reader = zip
			} else if response.Header.Get("Content-Encoding") != "" {
				t.Fatal("compressed despite refusing gzip")
			}
			body, err := io.ReadAll(reader)
			if err != nil || string(body) != code {
				t.Fatal("code changed during compression")
			}
			if response.Header.Get("Vary") != "Accept-Encoding" {
				t.Fatal("negotiation missing")
			}
		})
	}
}

func TestStaticCompressionDoesNotChangeRPCOrRanges(t *testing.T) {
	for _, item := range []struct {
		path, method, kind string
		status             int
		encoded, ranged    bool
	}{
		{"/api/session/modelCatalog", "GET", "application/json", 200, false, false},
		{"/plugins/", "POST", "application/javascript", 200, false, false},
		{"/assets/code.js", "GET", "application/javascript", 206, false, false},
		{"/assets/code.js", "GET", "application/javascript", 200, true, false},
		{"/assets/code.js", "GET", "application/javascript", 200, false, true},
		{"/", "GET", "text/html", 200, false, false},
	} {
		response := &http.Response{StatusCode: item.status, Request: httptest.NewRequest(item.method, "https://example.test"+item.path, nil), Header: http.Header{"Content-Type": {item.kind}}, Body: io.NopCloser(strings.NewReader("original"))}
		if item.encoded {
			response.Header.Set("Content-Encoding", "br")
		}
		if item.ranged {
			response.Request.Header.Set("Range", "bytes=0-1")
		}
		original := response.Body
		compressStaticResponse(response, "gzip")
		if response.Body != original {
			t.Fatal("non-static response changed")
		}
		response.Body.Close()
	}
}

func TestStaticCompressionClientCloseReleasesUpstream(t *testing.T) {
	original, producer := io.Pipe()
	defer producer.Close()
	response := &http.Response{StatusCode: 200, Request: httptest.NewRequest("GET", "https://example.test/plugins/", nil), Header: http.Header{"Content-Type": {"text/javascript"}}, Body: original}
	compressStaticResponse(response, "gzip")
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := producer.Write([]byte("code")); err == nil {
		t.Fatal("client disconnect left upstream open")
	}
}

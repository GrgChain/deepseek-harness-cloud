package access

import (
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// Compress only authenticated static code, after compatibility adaptation.
// Session/RPC responses, range responses and upgrades retain their wire format.
func compressStaticResponse(response *http.Response, accepted string) {
	r := response.Request
	if r == nil || r.Method != http.MethodGet || response.StatusCode != http.StatusOK || response.Header.Get("Content-Encoding") != "" || response.Header.Get("Content-Range") != "" || r.Header.Get("Range") != "" || (r.URL.Path != "/plugins/" && !strings.HasPrefix(r.URL.Path, "/assets/")) {
		return
	}
	kind, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || (kind != "application/javascript" && kind != "text/javascript" && kind != "text/css") {
		return
	}
	response.Header.Add("Vary", "Accept-Encoding")
	if !acceptsGzip(accepted) {
		return
	}
	original := response.Body
	reader, writer := io.Pipe()
	response.Body = &compressedStaticBody{PipeReader: reader, original: original}
	response.ContentLength = -1
	response.Header.Del("Content-Length")
	response.Header.Del("ETag")
	response.Header.Del("Accept-Ranges")
	response.Header.Set("Content-Encoding", "gzip")
	go func() {
		defer original.Close()
		zip, _ := gzip.NewWriterLevel(writer, gzip.BestSpeed)
		_, err := io.Copy(zip, original)
		if err == nil {
			err = zip.Close()
		}
		_ = writer.CloseWithError(err)
	}()
}

type compressedStaticBody struct {
	*io.PipeReader
	original io.Closer
}

func (body *compressedStaticBody) Close() error {
	err := body.PipeReader.Close()
	_ = body.original.Close()
	return err
}

func acceptsGzip(value string) bool {
	for _, part := range strings.Split(value, ",") {
		fields := strings.Split(part, ";")
		if !strings.EqualFold(strings.TrimSpace(fields[0]), "gzip") {
			continue
		}
		quality := 1.0
		for _, parameter := range fields[1:] {
			name, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if !ok || !strings.EqualFold(name, "q") {
				return false
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil || !(parsed >= 0 && parsed <= 1) {
				return false
			}
			quality = parsed
		}
		return quality > 0
	}
	return false
}

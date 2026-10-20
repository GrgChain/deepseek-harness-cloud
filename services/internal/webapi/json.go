package webapi

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
)

type Problem struct {
	Status        int
	Code, Message string
}

func (p *Problem) Error() string                  { return p.Code }
func Fail(status int, code, message string) error { return &Problem{status, code, message} }
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	p := &Problem{503, "DEPENDENCY_UNAVAILABLE", "平台依赖暂不可用，请稍后重试"}
	var supplied *Problem
	if errors.As(err, &supplied) {
		p = supplied
	}
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": p.Code, "message": p.Message, "request_id": w.Header().Get("X-Request-ID")})
}
func JSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func ID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("secure random source unavailable")
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmtID(b)
}
func fmtID(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 36)
	j := 0
	for i := range out {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			out[i] = '-'
			continue
		}
		v := b[j/2]
		if j%2 == 0 {
			out[i] = digits[v>>4]
		} else {
			out[i] = digits[v&15]
		}
		j++
	}
	return string(out)
}

// StrictUnmarshal rejects duplicate keys, nulls, trailing data and unknown fields.
func StrictUnmarshal(data []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return errors.New("JSON object required")
	}
	if err := object(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
func object(d *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("JSON nesting exceeds limit")
	}
	keys := map[string]bool{}
	for d.More() {
		k, err := d.Token()
		if err != nil {
			return err
		}
		name, ok := k.(string)
		if !ok || keys[name] {
			return errors.New("duplicate or invalid JSON key")
		}
		keys[name] = true
		if err := value(d, depth+1); err != nil {
			return err
		}
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') {
		return errors.New("invalid JSON object")
	}
	return nil
}
func value(d *json.Decoder, depth int) error {
	if depth > 32 {
		return errors.New("JSON nesting exceeds limit")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	if token == nil {
		return errors.New("JSON null is not accepted")
	}
	if delim, ok := token.(json.Delim); ok {
		switch delim {
		case '{':
			return object(d, depth)
		case '[':
			for d.More() {
				if err := value(d, depth+1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return errors.New("invalid JSON array")
			}
		default:
			return errors.New("invalid JSON delimiter")
		}
	}
	return nil
}
func Decode(w http.ResponseWriter, r *http.Request, target any) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return Fail(422, "INVALID_SPEC", "请求必须使用 JSON 对象")
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return Fail(413, "PAYLOAD_TOO_LARGE", "请求内容超过 64KiB")
		}
		return Fail(422, "INVALID_SPEC", "无法读取请求内容")
	}
	if err := StrictUnmarshal(body, target); err != nil {
		return Fail(422, "INVALID_SPEC", "JSON 字段、类型或格式不合法")
	}
	return nil
}

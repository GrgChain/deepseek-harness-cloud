package contextservice

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

const maxDocumentBytes = 16 << 20

func parseDocument(w http.ResponseWriter, r *http.Request) (any, error) {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "multipart/form-data" {
		return nil, invalid("请选择文档文件")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentBytes+(64<<10))
	reader, err := r.MultipartReader()
	if err != nil {
		return nil, invalid("无法读取上传的文件")
	}
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		return nil, invalid("请选择文档文件")
	}
	name := filepath.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".txt", ".text", ".md", ".markdown", ".csv", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".odt", ".ods", ".odp", ".pdf", ".png", ".jpg", ".jpeg", ".webp", ".bmp", ".tif", ".tiff":
	default:
		return nil, invalid("不支持此文件格式")
	}
	if ext == ".markdown" {
		name = strings.TrimSuffix(name, filepath.Ext(name)) + ".md"
	}
	data, err := io.ReadAll(io.LimitReader(part, maxDocumentBytes+1))
	if err != nil {
		return nil, invalid("无法读取上传的文件")
	}
	if len(data) == 0 {
		return nil, invalid("文件为空")
	}
	if len(data) > maxDocumentBytes {
		return nil, webapi.Fail(413, "PAYLOAD_TOO_LARGE", "文件不能超过 16 MiB")
	}
	if next, err := reader.NextPart(); err != io.EOF || next != nil {
		return nil, invalid("一次只能选择一个文件")
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("files", name)
	if err != nil {
		return nil, unavailable()
	}
	if _, err = file.Write(data); err != nil {
		return nil, unavailable()
	}
	for key, value := range map[string]string{"to_formats": "md", "do_ocr": "true", "image_export_mode": "placeholder", "ocr_engine": "rapidocr", "ocr_lang": "ch"} {
		if err = form.WriteField(key, value); err != nil {
			return nil, unavailable()
		}
	}
	if err = form.Close(); err != nil {
		return nil, unavailable()
	}
	endpoint := os.Getenv("HC_DOCLING_URL")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:18582"
	}
	request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(endpoint, "/")+"/v1/convert/file", &body)
	if err != nil {
		return nil, unavailable()
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	client := &http.Client{Timeout: 290 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return nil, webapi.Fail(503, "CONTEXT_INVALID", "文档解析服务暂不可用，请稍后重试")
	}
	defer response.Body.Close()
	if response.StatusCode >= 500 {
		return nil, webapi.Fail(503, "CONTEXT_INVALID", "文档解析服务暂不可用，请稍后重试")
	}
	if response.StatusCode != http.StatusOK {
		return nil, invalid("文档解析失败，请检查文件格式及内容")
	}
	var result struct {
		Status   string `json:"status"`
		Document struct {
			Markdown string `json:"md_content"`
		} `json:"document"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result); err != nil {
		return nil, unavailable()
	}
	if result.Status != "success" {
		return nil, invalid("文档解析未完成，请检查文件或拆分后重试")
	}
	text := strings.TrimSpace(strings.ReplaceAll(result.Document.Markdown, "\x00", ""))
	if text == "" {
		return nil, invalid("未识别到文字，请选择包含文字的文件")
	}
	if !utf8.ValidString(text) || len(text) > 256<<10 {
		return nil, invalid("提取的文字超过 256 KiB，请拆分文件后重试")
	}
	return map[string]string{"content": text}, nil
}

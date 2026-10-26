// mcp-fixture is an independent synthetic test service, never a DSH extension.
package main

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoInput struct {
	Text string `json:"text"`
}
type writeInput struct {
	Text         string `json:"text"`
	DelaySeconds int    `json:"delay_seconds,omitempty"`
}

func main() {
	data, err := os.ReadFile(os.Getenv("HC_MCP_FIXTURE_KEY_FILE"))
	if err != nil || len(strings.TrimSpace(string(data))) < 32 {
		panic("private fixture key file required")
	}
	key := strings.TrimSpace(string(data))
	clear(data)
	server := mcp.NewServer(&mcp.Implementation{Name: "Portal MCP Test Service", Version: "1.0.0"}, nil)
	readOnly := false
	writes := &atomic.Int64{}
	mcp.AddTool(server, &mcp.Tool{Name: "echo", Description: "返回输入的合成测试文本，不修改数据", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &readOnly}}, func(ctx context.Context, r *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: in.Text}}}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "synthetic_write", Description: "增加测试服务内存计数，延迟返回；仅用于验证未知结果保护"}, func(ctx context.Context, r *mcp.CallToolRequest, in writeInput) (*mcp.CallToolResult, any, error) {
		count := writes.Add(1)
		delay := in.DelaySeconds
		if delay < 0 {
			delay = 0
		}
		if delay > 120 {
			delay = 120
		}
		select {
		case <-time.After(time.Duration(delay) * time.Second):
		case <-ctx.Done():
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("synthetic effect %d: %s", count, in.Text)}}}, nil, nil
	})
	mcp.AddTool(server, &mcp.Tool{Name: "effect_count", Description: "读取合成写入次数", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &readOnly}}, func(ctx context.Context, r *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprint(writes.Load())}}}, nil, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+key)) != 1 {
			http.Error(w, "Unauthorized", 401)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	addr := os.Getenv("HC_MCP_FIXTURE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:18781"
	}
	httpServer := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second, MaxHeaderBytes: 32 << 10}
	if err = httpServer.ListenAndServe(); err != nil {
		panic("fixture server stopped")
	}
}

package access

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

//go:embed managed-directory.js
var managedDirectoryView string

// Scope the browser picker to the managed workspace tree. This is navigation
// policy; container filesystem permissions and server RPC checks are unchanged.
func adaptManagedDirectory(body []byte) ([]byte, bool) {
	const startText = "window.__ModuleLoader__.load({\n\tid: \"@deepseek-ai/dsh-client-ui-directory-picker-browse\","
	const size = 49640
	const digest = "6505736ae8b259042476c7a352d0083dc3795cadd7688ce3b435eca697b7b806"
	start := bytes.Index(body, []byte(startText))
	if start < 0 || len(body)-start < size {
		return body, false
	}
	module := body[start : start+size]
	sum := sha256.Sum256(module)
	if hex.EncodeToString(sum[:]) != digest {
		return body, false
	}
	old := []byte("const injected = () => ({\n\t\t\t\tlistDirectory: (path, signal) => ctx.uiWorkspace.listDirectory(path, signal),\n\t\t\t\tcreateDirectory: (path, name) => ctx.uiWorkspace.createDirectory(path, name),\n\t\t\t\tt: ctx.locale.bind(LOCALE_NS)\n\t\t\t});")
	if bytes.Count(module, old) != 1 {
		return body, false
	}
	patched := bytes.Replace(module, old, []byte(managedDirectoryView), 1)
	result := make([]byte, 0, len(body)+len(patched)-len(module))
	result = append(result, body[:start]...)
	result = append(result, patched...)
	result = append(result, body[start+size:]...)
	return result, true
}

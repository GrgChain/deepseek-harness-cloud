package access

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
)

// Only the reviewed shell module is adapted. Native connection recovery remains
// authoritative; first contact must not be presented as a recovered failure.
func adaptManagedConnection(body []byte) ([]byte, bool) {
	const marker = "window.__ModuleLoader__.load({\n\tid: \"@deepseek-ai/dsh-client-ui-settings-general\","
	const size = 52296
	const digest = "d23fcf98b016ff4eb867e19c2b6ea12384bb51093fd628b5abd0e358bf47ebef"
	start := bytes.Index(body, []byte(marker))
	if start < 0 || len(body)-start < size {
		return body, false
	}
	module := body[start : start+size]
	sum := sha256.Sum256(module)
	if hex.EncodeToString(sum[:]) != digest {
		return body, false
	}
	replacements := [][2]string{
		{"const previousConnectionState = (0, react.useRef)(connectionState);", "const previousConnectionState = (0, react.useRef)(connectionState);\nconst hasConnected = (0, react.useRef)(connectionState === \"connected\");"},
		{"if (previous !== \"disconnected\" && previous !== \"connecting\") return;", "const recovering = hasConnected.current; hasConnected.current = true;\nif (!recovering || (previous !== \"disconnected\" && previous !== \"connecting\")) return;"},
		{"if (connectionState === \"connecting\") {\n\t\t\t\t\tconnectingShownAt.current", "if (!hasConnected.current) return;\nif (connectionState === \"connecting\") {\n\t\t\t\t\tconnectingShownAt.current"},
		{"connectingLabel: t(\"connection.connecting\"),", "connectingLabel: t(hasConnected.current ? \"connection.connecting\" : \"connection.initializing\"),"},
		{"\"connection.connecting\": \"重新连接中\",", "\"connection.initializing\": \"正在连接工作空间…\",\n\"connection.connecting\": \"重新连接中\","},
		{"\"connection.connecting\": \"Reconnecting\",", "\"connection.initializing\": \"Connecting to workspace…\",\n\"connection.connecting\": \"Reconnecting\","},
	}
	patched := module
	for _, r := range replacements {
		if bytes.Count(patched, []byte(r[0])) != 1 {
			return body, false
		}
		patched = bytes.Replace(patched, []byte(r[0]), []byte(r[1]), 1)
	}
	result := make([]byte, 0, len(body)+len(patched)-len(module))
	result = append(result, body[:start]...)
	result = append(result, patched...)
	result = append(result, body[start+size:]...)
	return result, true
}

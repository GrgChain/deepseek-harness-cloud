package access

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// This browser-only compatibility adapter is pinned to the exact published
// dsh-client-ui-settings-models 0.2.1-alpha.2 module. It changes no server RPC,
// authorization or settings-write policy. Unknown modules pass through intact.
const managedModelsSHA = "681f4c16df2a14ef572e3ff3272cc5c75b5b6a48f0a4a63ea8ec87b88c876dfb"
const managedModelsSize = 188765
const modelsModuleStart = "window.__ModuleLoader__.load({\n\tid: \"@deepseek-ai/dsh-client-ui-settings-models\","

//go:embed managed-models-read.js
var managedModelsRead string

//go:embed managed-models-view.js
var managedModelsView string

func adaptManagedModels(body []byte, portal, instance string) ([]byte, bool) {
	start := bytes.Index(body, []byte(modelsModuleStart))
	if start < 0 || len(body)-start < managedModelsSize {
		return body, false
	}
	module := body[start : start+managedModelsSize]
	sum := sha256.Sum256(module)
	if hex.EncodeToString(sum[:]) != managedModelsSHA {
		return body, false
	}
	readAnchor := "\t\t\t\tconst [registered, declared] = await Promise.all(["
	viewAnchor := "\t\t\tconst savedRow = savedTarget === void 0 ? void 0 : state.rows.find((row) => row.entry.provider === savedTarget.provider);"
	if bytes.Count(module, []byte(readAnchor)) != 1 || bytes.Count(module, []byte(viewAnchor)) != 1 {
		return body, false
	}
	modelsURL, _ := json.Marshal(portal + "/app/models")
	instanceURL, _ := json.Marshal(portal + "/app/instances/" + instance)
	view := strings.NewReplacer("HC_MODELS_URL", string(modelsURL), "HC_INSTANCE_URL", string(instanceURL)).Replace(managedModelsView)
	patched := bytes.Replace(module, []byte(readAnchor), []byte(managedModelsRead+readAnchor), 1)
	// Share the selector's bounded, generation-aware catalog recovery. Superseding
	// a read or disposing the page must also release its pending retry timer.
	methodAnchor := []byte("\t\t\tasync read(generation) {")
	loadAnchor := []byte("const generation = ++this.generation;")
	constructorAnchor := []byte("this.describeFace = describeFace;")
	for _, anchor := range [][]byte{methodAnchor, loadAnchor, constructorAnchor} {
		if bytes.Count(module, anchor) != 1 {
			return body, false
		}
	}
	patched = bytes.Replace(patched, methodAnchor, append([]byte(managedModelCatalogRead+"\n"), methodAnchor...), 1)
	patched = bytes.Replace(patched, loadAnchor, []byte("clearTimeout(this.retryTimer); this.retryResolve?.(); this.retryTimer = this.retryResolve = undefined;\n"+string(loadAnchor)), 1)
	patched = bytes.Replace(patched, constructorAnchor, []byte(string(constructorAnchor)+"\nctx.on(\"dispose\", () => { this.generation++; clearTimeout(this.retryTimer); this.retryResolve?.(); this.retryTimer = this.retryResolve = undefined; for (const pending of this.pendingLoads) pending.resolve(); this.pendingLoads.clear(); });"), 1)
	patched = bytes.Replace(patched, []byte(viewAnchor), []byte(view+viewAnchor), 1)
	result := make([]byte, 0, len(body)+len(patched)-len(module))
	result = append(result, body[:start]...)
	result = append(result, patched...)
	result = append(result, body[start+managedModelsSize:]...)
	return result, true
}

func managedModelsResponse(response *http.Response, portal, instance string) error {
	r := response.Request
	if r == nil || r.Method != http.MethodGet || r.URL.Path != "/plugins/" || (!strings.Contains(r.URL.RawQuery, "dsh-client-ui-settings-models") && !strings.Contains(r.URL.RawQuery, "dsh-client-ui-directory-picker-browse") && !strings.Contains(r.URL.RawQuery, "dsh-client-ui-model-selection") && !strings.Contains(r.URL.RawQuery, "dsh-client-ui-settings-general")) || response.StatusCode != http.StatusOK || response.Header.Get("Content-Encoding") != "" {
		return nil
	}
	// Combined plugin responses may contain multiple packages. Bound buffering;
	// an unexpectedly large or unknown bundle continues as the original stream.
	const limit = 32 << 20
	original := response.Body
	body, err := io.ReadAll(io.LimitReader(original, limit+1))
	if err != nil {
		_ = original.Close()
		return err
	}
	if len(body) > limit {
		response.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(body), original), original}
		return nil
	}
	_ = original.Close()
	patched, changed := adaptManagedModels(body, portal, instance)
	patched, directoryChanged := adaptManagedDirectory(patched)
	patched, startupChanged := adaptManagedModelStartup(patched)
	patched, connectionChanged := adaptManagedConnection(patched)
	changed = changed || directoryChanged || startupChanged || connectionChanged
	response.Body = io.NopCloser(bytes.NewReader(patched))
	if changed {
		response.ContentLength = int64(len(patched))
		response.Header.Set("Content-Length", strconv.Itoa(len(patched)))
		response.Header.Del("ETag")
		response.Header.Del("Last-Modified")
	}
	return nil
}

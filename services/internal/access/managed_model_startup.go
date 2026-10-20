package access

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

//go:embed managed-model-catalog-read.js
var managedModelCatalogRead string

// The pinned client eagerly loads before connection/reset immediately reloads
// the same catalog. Let connection/reset and the selector's normal load start
// the initial request; reconnect invalidation and explicit refresh stay intact.
func adaptManagedModelStartup(body []byte) ([]byte, bool) {
	const marker = "window.__ModuleLoader__.load({\n\tid: \"@deepseek-ai/dsh-client-ui-model-selection\","
	const size = 78431
	const digest = "001048a0734f2559b991a268a94dd768563ef1ec8d8d20604e766aa71c6a04fa"
	start := bytes.Index(body, []byte(marker))
	if start < 0 || len(body)-start < size {
		return body, false
	}
	module := body[start : start+size]
	sum := sha256.Sum256(module)
	if hex.EncodeToString(sum[:]) != digest {
		return body, false
	}
	anchor := []byte("this.catalog = new ModelCatalogDirectory(ctx);\n\t\t\t\tthis.catalog.load().catch(() => {});")
	selector := []byte("const available = sessions.subagentAddress(sessionId) === void 0;\n\t\t\t\t\t\treturn {")
	if bytes.Count(module, anchor) != 1 || bytes.Count(module, selector) != 1 {
		return body, false
	}
	patched := bytes.Replace(module, anchor, []byte("this.catalog = new ModelCatalogDirectory(ctx);"), 1)
	loadAnchor := []byte("const operation = this.ctx.remote.session.modelCatalog().then((response) => {")
	invalidateAnchor := []byte("invalidate(clear = false) {\n\t\t\t\tthis.generation += 1;")
	methodAnchor := []byte("\t\t\tload() {")
	if bytes.Count(module, loadAnchor) != 1 || bytes.Count(module, invalidateAnchor) != 1 || bytes.Count(module, methodAnchor) != 1 {
		return body, false
	}
	patched = bytes.Replace(patched, loadAnchor, []byte("const operation = this.readManagedCatalog(generation).then((response) => {"), 1)
	patched = bytes.Replace(patched, methodAnchor, append([]byte(managedModelCatalogRead+"\n"), methodAnchor...), 1)
	patched = bytes.Replace(patched, invalidateAnchor, []byte("invalidate(clear = false) {\n\t\t\t\tthis.generation += 1; clearTimeout(this.retryTimer); this.retryResolve?.(); this.retryTimer = this.retryResolve = undefined;"), 1)
	patched = bytes.Replace(patched, []byte("this.catalog = new ModelCatalogDirectory(ctx);"), []byte("this.catalog = new ModelCatalogDirectory(ctx);\nctx.on(\"dispose\", () => this.catalog.invalidate(true));"), 1)
	patched = bytes.Replace(patched, selector, []byte("const available = sessions.subagentAddress(sessionId) === void 0;\n\t\t\t\t\t\tif (available) directory.load().catch(() => {});\n\t\t\t\t\t\treturn {"), 1)

	for _, replacement := range [][2]string{
		{`const modelLabel = waiting ? t("trigger.loading") : currentChoice?.model.name`, `const modelLabel = waiting ? t("trigger.loading") : state.current === null && state.status === "error" ? t("trigger.unavailable") : currentChoice?.model.name`},
		{`state.current === null ? t("trigger.selectAria") : effortLabel`, `state.current === null ? (state.status === "error" ? t("trigger.unavailable") : t("trigger.selectAria")) : effortLabel`},
		{`"trigger.loading": "正在加载模型…",`, `"trigger.loading": "正在加载模型…", "trigger.unavailable": "模型暂不可用，点击重试",`},
		{`"trigger.loading": "Loading models…",`, `"trigger.loading": "Loading models…", "trigger.unavailable": "Model unavailable, retry",`},
	} {
		if bytes.Count(patched, []byte(replacement[0])) != 1 {
			return body, false
		}
		patched = bytes.Replace(patched, []byte(replacement[0]), []byte(replacement[1]), 1)
	}
	result := make([]byte, 0, len(body)+len(patched)-len(module))
	result = append(result, body[:start]...)
	result = append(result, patched...)
	result = append(result, body[start+size:]...)
	return result, true
}

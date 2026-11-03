package plugins

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

const registryLimit = 1 << 20

var packageName = regexp.MustCompile(`^(?:@[a-z0-9-][a-z0-9._-]*/)?[a-z0-9-][a-z0-9._-]*$`)
var exactVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// Registry reads public package metadata only; it never downloads or runs a
// package. Client is injectable for tests, while every request URL stays fixed
// to npm's registry and redirects are disabled even for an injected client.
type Registry struct {
	Client *http.Client
}

type registryManifest struct {
	Name             string            `json:"name"`
	Version          string            `json:"version"`
	Description      string            `json:"description"`
	Keywords         []string          `json:"keywords"`
	Author           json.RawMessage   `json:"author"`
	Maintainers      []registryAuthor  `json:"maintainers"`
	Repository       json.RawMessage   `json:"repository"`
	Homepage         string            `json:"homepage"`
	Engines          map[string]string `json:"engines"`
	PeerDependencies map[string]string `json:"peerDependencies"`
	DSH              struct {
		Bundle struct {
			Patch string `json:"patch"`
		} `json:"bundle"`
		Engines map[string]string `json:"engines"`
	} `json:"dsh"`
}

type registryAuthor struct {
	Name string `json:"name"`
}

func parsePackageSpec(spec string) (string, string, error) {
	spec = strings.TrimSpace(spec)
	name, version := spec, "latest"
	if i := strings.LastIndex(spec, "@"); i > 0 {
		name, version = spec[:i], spec[i+1:]
	}
	if len(name) > 214 || !packageName.MatchString(name) || (version != "latest" && !fixedVersion(version)) {
		return "", "", webapi.Fail(422, "PLUGIN_INVALID", "请输入有效的 npm 包名，可附加准确版本，例如 @scope/plugin@1.2.3")
	}
	if strings.HasPrefix(name, "@deepseek-ai/") {
		return "", "", webapi.Fail(422, "PLUGIN_INVALID", "插件列表仅支持第三方插件")
	}
	return name, version, nil
}

func fixedVersion(value string) bool {
	if len(value) > 128 {
		return false
	}
	match := exactVersion.FindStringSubmatch(value)
	if match == nil {
		return false
	}
	for _, identifier := range strings.Split(match[4], ".") {
		if len(identifier) > 1 && identifier[0] == '0' && strings.Trim(identifier, "0123456789") == "" {
			return false
		}
	}
	return true
}

func (r Registry) Resolve(ctx context.Context, spec string) (Plugin, error) {
	name, version, err := parsePackageSpec(spec)
	if err != nil {
		return Plugin{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://registry.npmjs.org/"+url.PathEscape(name)+"/"+url.PathEscape(version), nil)
	if err != nil {
		return Plugin{}, registryUnavailable()
	}
	request.Header.Set("Accept", "application/json")
	client := http.Client{Timeout: 6 * time.Second}
	if r.Client != nil {
		client = *r.Client
		if client.Timeout <= 0 || client.Timeout > 6*time.Second {
			client.Timeout = 6 * time.Second
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return Plugin{}, registryUnavailable()
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return Plugin{}, webapi.Fail(404, "PLUGIN_NOT_FOUND", "未找到该 npm 包或版本，请检查包名与版本")
	}
	if response.StatusCode != http.StatusOK {
		return Plugin{}, registryUnavailable()
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, registryLimit+1))
	if err != nil || len(body) > registryLimit {
		return Plugin{}, registryUnavailable()
	}
	var manifest registryManifest
	if err := json.Unmarshal(body, &manifest); err != nil || manifest.Name != name || !fixedVersion(manifest.Version) || (version != "latest" && manifest.Version != version) {
		return Plugin{}, registryUnavailable()
	}
	plugin, _, err := pluginFromManifest(manifest)
	if err == nil {
		plugin.AdditionMethod = "online"
		plugin.SourceNote = "包信息及 DSH bundle 声明"
	}
	return plugin, err
}

// Online metadata and offline packages use the same identity, bundle and public
// metadata rules. The returned patch path is relative to the package root.
func pluginFromManifest(manifest registryManifest) (Plugin, string, error) {
	name, version, err := parsePackageSpec(manifest.Name + "@" + manifest.Version)
	if err != nil {
		return Plugin{}, "", err
	}
	if name != manifest.Name || version != manifest.Version || !fixedVersion(manifest.Version) {
		return Plugin{}, "", invalid("插件包须声明有效包名和固定版本")
	}
	patch := strings.TrimPrefix(strings.TrimSpace(manifest.DSH.Bundle.Patch), "./")
	if patch == "" || len(patch) > 512 || path.IsAbs(patch) || strings.ContainsAny(patch, "\\:") || strings.IndexFunc(patch, unicode.IsControl) >= 0 || path.Clean(patch) == "." || path.Clean(patch) == ".." || strings.HasPrefix(path.Clean(patch), "../") {
		return Plugin{}, "", webapi.Fail(422, "PLUGIN_INVALID", "该 npm 包未声明有效的 DSH 插件组合包")
	}
	var author registryAuthor
	if json.Unmarshal(manifest.Author, &author) != nil {
		_ = json.Unmarshal(manifest.Author, &author.Name)
	}
	if author.Name == "" && len(manifest.Maintainers) > 0 {
		author = manifest.Maintainers[0]
	}
	author.Name = registryText(author.Name, 200)
	if author.Name == "" {
		author.Name = "未提供发布方"
	}
	var repository struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(manifest.Repository, &repository) != nil {
		_ = json.Unmarshal(manifest.Repository, &repository.URL)
	}
	repo := registryURL(strings.TrimSuffix(strings.TrimPrefix(repository.URL, "git+"), ".git"))
	documentation := registryURL(manifest.Homepage)
	if documentation == "" {
		documentation = repo
	}
	if documentation == "" {
		documentation = "https://www.npmjs.com/package/" + name + "/v/" + manifest.Version
	}
	dshVersion := manifest.DSH.Engines["dsh"]
	if dshVersion == "" {
		dshVersion = manifest.Engines["dsh"]
	}
	if dshVersion == "" {
		dshVersion = manifest.PeerDependencies["@deepseek-ai/dsh"]
	}
	compatibility := "未声明 DSH 版本范围，需由 DSH 检查依赖兼容性。"
	if dshVersion = registryText(dshVersion, 300); dshVersion != "" {
		compatibility = "包声明 DSH " + dshVersion + "；需由 DSH 检查依赖兼容性。"
	}
	if nodeVersion := registryText(manifest.Engines["node"], 300); nodeVersion != "" {
		compatibility += "Node.js 要求 " + nodeVersion + "。"
	}
	keywords := make([]string, 0, min(len(manifest.Keywords), 20))
	for _, keyword := range manifest.Keywords {
		if keyword = registryText(keyword, 60); keyword != "" {
			keywords = append(keywords, keyword)
		}
		if len(keywords) == 20 {
			break
		}
	}
	description := registryText(manifest.Description, 4000)
	if description == "" {
		description = "未提供插件说明。"
	}
	return Plugin{Name: name, PackageName: name, Version: manifest.Version, Source: "community", Author: author.Name,
		Category: "集成扩展", Summary: registryText(description, 180), Description: description, Keywords: keywords,
		Repository: repo, Documentation: documentation, Compatibility: compatibility,
		CheckedAt: time.Now().UTC().Format("2006-01-02"), Custom: true}, path.Clean(patch), nil
}

func registryUnavailable() error {
	return webapi.Fail(503, "DEPENDENCY_UNAVAILABLE", "暂时无法读取 npm 包信息，请稍后重试")
}

func registryText(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	text := []rune(strings.TrimSpace(value))
	if len(text) > limit {
		text = text[:limit]
	}
	return string(text)
}

func registryURL(value string) string {
	if len(value) > 2048 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return ""
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return ""
	}
	return u.String()
}

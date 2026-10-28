package skills

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/config"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
)

type HubSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Publisher   string `json:"publisher"`
	URL         string `json:"url"`
	Downloads   int64  `json:"downloads"`
	Metric      string `json:"metric,omitempty"`
	SourceURL   string `json:"source_url,omitempty"`
}

type HubSearch struct {
	Items    []HubSkill `json:"items"`
	Provider string     `json:"provider"`
}

func hubClient() (*http.Client, func(), error) {
	dial, err := configuredSourceDialer()
	if err != nil {
		return nil, nil, invalid("技能来源网络配置不可用，请联系管理员")
	}
	transport := sourceTransport(net.DefaultResolver.LookupNetIP, dial)
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, transport.CloseIdleConnections, nil
}

func (s *Service) SearchHub(ctx context.Context, p identity.Principal, provider, query string) (HubSearch, error) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) < 2 || utf8.RuneCountInString(query) > 100 || strings.ContainsFunc(query, unicode.IsControl) {
		return HubSearch{}, invalid("请输入 2–100 个字符的搜索关键词")
	}
	if provider != "skills.sh" && provider != "clawhub" && provider != "tencent" && provider != "skillsmp" {
		return HubSearch{}, invalid("请选择支持的技能市场")
	}
	tx, err := s.begin(ctx, p, false)
	if err != nil {
		return HubSearch{}, err
	}
	defer tx.Rollback(context.Background())
	if err = identity.RequirePermission(ctx, tx, p, "skills:manage"); err != nil {
		return HubSearch{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return HubSearch{}, err
	}
	client, close, err := hubClient()
	if err != nil {
		return HubSearch{}, err
	}
	defer close()
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	return searchHub(ctx, client, provider, query)
}

// Hub requests only reach fixed public registries through the import transport.
// Credentials are never forwarded. Redirects, private DNS and oversized replies
// are rejected by the same bounded downloader used for source imports.
func hubAPIURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Port() != "" {
		return nil, invalid("技能市场地址无效")
	}
	if u.Host == "skills.sh" && u.Path == "/api/search" || u.Host == "clawhub.ai" && (u.Path == "/api/v1/search" || u.Path == "/api/v1/download") {
		return u, nil
	}
	if u.Host == "api.skillhub.cn" && (u.Path == "/api/skills" || u.Path == "/api/v1/download") || u.Host == "skillsmp.com" && u.Path == "/api/v1/skills/search" {
		return u, nil
	}
	return nil, invalid("技能市场地址无效")
}

func searchHub(ctx context.Context, client *http.Client, provider, query string) (HubSearch, error) {
	endpoint := "https://skills.sh/api/search"
	params := url.Values{"q": {query}, "limit": {"20"}}
	switch provider {
	case "skills.sh":
	case "clawhub":
		endpoint = "https://clawhub.ai/api/v1/search"
	case "tencent":
		endpoint = "https://api.skillhub.cn/api/skills"
		params = url.Values{"keyword": {query}, "pageSize": {"20"}}
	case "skillsmp":
		endpoint = "https://skillsmp.com/api/v1/skills/search"
	default:
		return HubSearch{}, invalid("技能市场无效")
	}
	u, _ := url.Parse(endpoint)
	u.RawQuery = params.Encode()
	data, _, err := downloadHub(ctx, client, u, 1<<20, hubAPIURL)
	if err != nil {
		return HubSearch{}, invalid("技能市场搜索暂不可用或已达到限额，请稍后重试或切换来源")
	}
	out := HubSearch{Items: []HubSkill{}, Provider: provider}
	if provider == "skills.sh" {
		var response struct {
			Skills []struct {
				Name     string `json:"name"`
				Source   string `json:"source"`
				SkillID  string `json:"skillId"`
				Installs int64  `json:"installs"`
			} `json:"skills"`
		}
		if json.Unmarshal(data, &response) != nil || response.Skills == nil {
			return out, invalid("技能市场返回了无效结果，请稍后重试")
		}
		for _, item := range response.Skills {
			slug := item.SkillID
			if slug == "" {
				slug = item.Name
			}
			link := "https://skills.sh/" + item.Source + "/" + slug
			if _, err := hubSourceURL(link); err != nil {
				continue
			}
			out.Items = append(out.Items, HubSkill{Name: item.Name, Publisher: item.Source, URL: link, Downloads: item.Installs})
			if len(out.Items) == 20 {
				break
			}
		}
	} else if provider == "clawhub" {
		var response struct {
			Results []struct {
				Slug      string `json:"slug"`
				Name      string `json:"displayName"`
				Summary   string `json:"summary"`
				Owner     string `json:"ownerHandle"`
				Downloads int64  `json:"downloads"`
			} `json:"results"`
		}
		if json.Unmarshal(data, &response) != nil || response.Results == nil {
			return out, invalid("技能市场返回了无效结果，请稍后重试")
		}
		for _, item := range response.Results {
			link := "https://clawhub.ai/" + item.Owner + "/skills/" + item.Slug
			if _, err := hubSourceURL(link); err != nil {
				continue
			}
			out.Items = append(out.Items, HubSkill{Name: item.Name, Description: item.Summary, Publisher: item.Owner, URL: link, Downloads: item.Downloads})
			if len(out.Items) == 20 {
				break
			}
		}
	} else if provider == "tencent" {
		var response struct {
			Code int `json:"code"`
			Data struct {
				Skills []struct {
					Slug        string `json:"slug"`
					Name        string `json:"name"`
					Description string `json:"description"`
					Chinese     string `json:"description_zh"`
					Owner       string `json:"ownerName"`
					Downloads   int64  `json:"downloads"`
				} `json:"skills"`
			} `json:"data"`
		}
		if json.Unmarshal(data, &response) != nil || response.Code != 0 || response.Data.Skills == nil {
			return out, invalid("SkillHub 返回了无效结果，请稍后重试")
		}
		for _, item := range response.Data.Skills {
			if !validName(item.Slug) {
				continue
			}
			link := "https://skillhub.cn/skills/" + item.Slug
			description := item.Chinese
			if description == "" {
				description = item.Description
			}
			out.Items = append(out.Items, HubSkill{Name: item.Name, Description: description, Publisher: item.Owner, URL: link, Downloads: item.Downloads})
			if len(out.Items) == 20 {
				break
			}
		}
	} else {
		var response struct {
			Success bool `json:"success"`
			Data    struct {
				Skills []struct {
					Name        string `json:"name"`
					Description string `json:"description"`
					Author      string `json:"author"`
					GitHubURL   string `json:"githubUrl"`
					SkillURL    string `json:"skillUrl"`
					Stars       int64  `json:"stars"`
				} `json:"skills"`
			} `json:"data"`
		}
		if json.Unmarshal(data, &response) != nil || !response.Success || response.Data.Skills == nil {
			return out, invalid("SkillsMP 返回了无效结果，请稍后重试")
		}
		for _, item := range response.Data.Skills {
			source, err := sourceURL(item.GitHubURL)
			if err != nil || source.Host != "github.com" && source.Host != "raw.githubusercontent.com" {
				continue
			}
			link, err := url.Parse(item.SkillURL)
			if err != nil || link.Scheme != "https" || link.Host != "skillsmp.com" || link.User != nil || link.RawQuery != "" || link.Fragment != "" || !strings.HasPrefix(link.Path, "/creators/") {
				continue
			}
			out.Items = append(out.Items, HubSkill{Name: item.Name, Description: item.Description, Publisher: item.Author, URL: link.String(), SourceURL: source.String(), Downloads: item.Stars, Metric: "stars"})
			if len(out.Items) == 20 {
				break
			}
		}

	}
	return out, nil
}

func hubSourceURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.Port() != "" {
		return nil, invalid("技能市场链接无效")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if (u.Host == "skillhub.cn" || u.Host == "api.skillhub.cn") && len(parts) == 2 && safePath(parts[0]) && validName(parts[1]) {
		// Use the website's /skills/{slug} route. Legacy homepage URLs use
		// /{owner}/{slug}, which does not render a skill detail page.
		u.Host = "skillhub.cn"
		u.Path = "/skills/" + parts[1]
		u.RawPath = ""
		return u, nil
	}
	if len(parts) != 3 || !githubOwner.MatchString(parts[0]) || !validName(parts[2]) {
		return nil, invalid("技能市场链接无效")
	}
	if u.Host == "skills.sh" && githubRepo.MatchString(parts[1]) && parts[1] != "." && parts[1] != ".." || u.Host == "clawhub.ai" && parts[1] == "skills" {
		return u, nil
	}
	return nil, invalid("技能市场链接无效")
}

func fetchHubSource(ctx context.Context, client *http.Client, u *url.URL) ([]byte, string, error) {
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if u.Host == "skillhub.cn" || u.Host == "api.skillhub.cn" {
		endpoint, _ := url.Parse("https://api.skillhub.cn/api/v1/download")
		endpoint.RawQuery = url.Values{"slug": {parts[1]}}.Encode()
		data, _, err := downloadHub(ctx, client, endpoint, 0, tencentDownloadURL)
		if err != nil {
			return nil, "", invalid("SkillHub 技能下载失败，请稍后重试或查看来源")
		}
		return data, u.String(), nil
	}
	if u.Host == "skills.sh" {
		location := githubSource{owner: parts[0], repo: parts[1]}
		content, err := fetchGitHubSource(ctx, client, location, parts[2])
		if err != nil {
			return nil, "", err
		}
		return content, u.String(), nil
	}
	endpoint, _ := url.Parse("https://clawhub.ai/api/v1/download")
	endpoint.RawQuery = url.Values{"slug": {parts[2]}, "ownerHandle": {parts[0]}}.Encode()
	data, _, err := downloadSource(ctx, client, endpoint, 0, hubAPIURL)
	if err != nil {
		return nil, "", invalid("该 ClawHub 技能暂不可下载，请查看来源或稍后重试")
	}
	if json.Valid(data) {
		return nil, "", invalid("该技能需要从 GitHub 来源导入，请在 ClawHub 来源页获取 GitHub 链接后使用链接导入")
	}
	return data, u.String(), nil
}

type hubKeyTransport struct {
	base              http.RoundTripper
	host, header, key string
}

func (t hubKeyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.Header = r.Header.Clone()
	if r.URL.Host == t.host {
		copy.Header.Set(t.header, t.key)
	}
	return t.base.RoundTrip(copy)
}

func downloadHub(ctx context.Context, client *http.Client, u *url.URL, limit int, validate func(string) (*url.URL, error)) ([]byte, string, error) {
	keyFile, header, prefix := "", "", ""
	switch u.Host {
	case "api.skillhub.cn":
		keyFile = os.Getenv("HC_TENCENT_SKILLHUB_API_KEY_FILE")
		header = "X-API-Key"
	case "skillsmp.com":
		keyFile = os.Getenv("HC_SKILLSMP_API_KEY_FILE")
		header = "Authorization"
		prefix = "Bearer "
	}
	if keyFile == "" {
		return downloadSource(ctx, client, u, limit, validate)
	}
	key, err := config.ReadSecret(keyFile)
	if err != nil || len(key) > 4096 {
		return nil, "", invalid("技能市场密钥配置不可用，请联系管理员")
	}
	copy := *client
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	copy.Transport = hubKeyTransport{base: base, host: u.Host, header: header, key: prefix + key}
	return downloadSource(ctx, &copy, u, limit, validate)
}

// Signed artifact URLs are kept only for this download; every hop still uses
// public DNS checks and pinned TLS. Only Tencent COS storage is accepted.
func tencentDownloadURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Port() != "" {
		return nil, invalid("SkillHub 技能下载地址无效")
	}
	if u.Host == "api.skillhub.cn" && u.Path == "/api/v1/download" {
		return u, nil
	}
	if strings.HasSuffix(u.Host, ".myqcloud.com") && strings.Contains(u.Host, ".cos.") && strings.HasSuffix(strings.ToLower(u.Path), ".zip") {
		return u, nil
	}
	return nil, invalid("SkillHub 技能下载重定向无效")
}

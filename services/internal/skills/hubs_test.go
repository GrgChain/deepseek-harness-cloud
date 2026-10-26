package skills

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type hubRoundTripper func(*http.Request) (*http.Response, error)

func (f hubRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHubSourceBoundaries(t *testing.T) {
	for _, raw := range []string{"https://skills.sh/team/repo/browser", "https://clawhub.ai/publisher/skills/browser", "https://api.skillhub.cn/user_team/browser", "https://skillhub.cn/indiv-weishui/ws-bond-plan"} {
		if _, err := sourceURL(raw); err != nil {
			t.Fatal(raw, err)
		}
	}
	for _, raw := range []string{"https://skills.sh/team/../browser", "https://skills.sh/team/repo/browser?token=x", "https://skills.sh/team/repo/browser#x", "https://skills.sh/team/repo/%2fprivate", "https://clawhub.ai/publisher/download/browser", "https://clawhub.ai/api/v1/download?slug=browser", "https://skills.sh:444/team/repo/browser", "https://clawhub.ai.evil.test/publisher/skills/browser", "https://skills.sh/team/repo/browser/extra"} {
		if _, err := sourceURL(raw); err == nil {
			t.Fatal("accepted invalid hub source", raw)
		}
	}
	for _, raw := range []string{"https://127.0.0.1/api/search?q=x", "https://skills.sh.evil.test/api/search", "https://clawhub.ai/api/v1/skills", "https://user@skills.sh/api/search", "http://skills.sh/api/search"} {
		if _, err := hubAPIURL(raw); err == nil {
			t.Fatal("accepted invalid registry redirect", raw)
		}
	}
}

func TestHubSearchNormalizesPublicResults(t *testing.T) {
	for _, provider := range []string{"skills.sh", "clawhub", "tencent", "skillsmp"} {
		t.Run(provider, func(t *testing.T) {
			client := &http.Client{Transport: hubRoundTripper(func(r *http.Request) (*http.Response, error) {
				q, limit := r.URL.Query().Get("q"), r.URL.Query().Get("limit")
				if provider == "tencent" {
					q, limit = r.URL.Query().Get("keyword"), r.URL.Query().Get("pageSize")
				}
				if r.Method != "GET" || q != "browser & 中文" || limit != "20" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Fatal("incorrect upstream request", r)
				}
				if _, err := hubAPIURL(r.URL.String()); err != nil {
					t.Fatal(err)
				}
				body := `{"skills":[{"name":"browser","skillId":"browser","source":"team/repo","installs":17},{"name":"bad","source":"../evil"}]}`
				if provider == "clawhub" {
					body = `{"results":[{"displayName":"Browser","slug":"browser","ownerHandle":"team","summary":"Browse sites","downloads":17},{"slug":"bad","ownerHandle":"../evil"}]}`
				}
				if provider == "tencent" {
					body = `{"code":0,"data":{"skills":[{"name":"Browser","slug":"browser","ownerName":"team","description":"English","description_zh":"中文描述","homepage":"https://api.skillhub.cn/user_team/browser","downloads":17},{"slug":"../bad","homepage":"https://evil.test/team/bad"}]}}`
				}
				if provider == "skillsmp" {
					body = `{"success":true,"data":{"skills":[{"name":"browser","author":"team","description":"Browse sites","githubUrl":"https://github.com/team/repo/tree/main/skills/browser","skillUrl":"https://skillsmp.com/creators/team/repo/skills-browser","stars":17},{"name":"bad","githubUrl":"https://127.0.0.1/skill.md","skillUrl":"https://skillsmp.com/creators/team/bad"}]}}`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			out, err := searchHub(context.Background(), client, provider, "browser & 中文")
			if err != nil || len(out.Items) != 1 || out.Items[0].Downloads != 17 || out.Provider != provider {
				t.Fatal("invalid normalized results", out, err)
			}
			link := out.Items[0].URL
			if provider == "skillsmp" {
				link = out.Items[0].SourceURL
				if out.Items[0].Metric != "stars" {
					t.Fatal("stars mislabeled")
				}
			}
			if _, err = sourceURL(link); err != nil {
				t.Fatal("result cannot be imported", err)
			}
			if provider == "tencent" && (out.Items[0].Description != "中文描述" || out.Items[0].URL != "https://skillhub.cn/skills/browser") {
				t.Fatal("Chinese description or website source link missing", out.Items[0])
			}
		})
	}
	for _, body := range []string{`{"skills":null}`, `{"unexpected":[]}`, `<html>down</html>`, strings.Repeat("x", (1<<20)+1)} {
		client := &http.Client{Transport: hubRoundTripper(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
		})}
		if _, err := searchHub(context.Background(), client, "skills.sh", "test"); err == nil {
			t.Fatal("invalid upstream response accepted")
		}
	}
}

func TestTencentSourceLinksUseWebsiteAndDownloadAPI(t *testing.T) {
	const website = "https://skillhub.cn/skills/ws-bond-plan"
	archive := zipFiles(t, map[string][]byte{"SKILL.md": markdown("ws-bond-plan", "Bond plan")}, 0600)
	for _, raw := range []string{website, "https://api.skillhub.cn/indiv-weishui/ws-bond-plan", "https://skillhub.cn/indiv-weishui/ws-bond-plan"} {
		u, err := sourceURL(raw)
		if err != nil || u.String() != website {
			t.Fatal("source must use the working detail page", raw, u, err)
		}
		calls := 0
		client := &http.Client{Transport: hubRoundTripper(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.Method != "GET" || r.URL.Host != "api.skillhub.cn" || r.URL.Path != "/api/v1/download" || r.URL.Query().Get("slug") != "ws-bond-plan" {
				t.Fatal("import must use the download API, not the detail page", r.URL)
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(archive))}, nil
		})}
		data, source, err := fetchHubSource(context.Background(), client, u)
		if err != nil || source != website || !bytes.Equal(data, archive) || calls != 1 {
			t.Fatal("website and legacy links must import the same package", source, err, calls)
		}
	}
	for _, raw := range []string{"https://skillhub.cn.evil.test/indiv-weishui/ws-bond-plan", "https://skillhub.cn/indiv-weishui/ws-bond-plan?token=x", "https://skillhub.cn/api/v1/download", "https://skillhub.cn/../ws-bond-plan"} {
		if _, err := sourceURL(raw); err == nil {
			t.Fatal("unsafe website link accepted", raw)
		}
	}
}

func TestTencentArtifactRedirectAndKeyIsolation(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "api-key")
	if err := os.WriteFile(keyFile, []byte("test-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HC_TENCENT_SKILLHUB_API_KEY_FILE", keyFile)
	archive := zipFiles(t, map[string][]byte{"SKILL.md": markdown("browser", "Browser"), "scripts/run.py": []byte("print('ok')")}, 0600)
	for _, target := range []string{"https://bucket.cos.accelerate.myqcloud.com/skills/browser/1.0.0.zip?sign=short-lived", "https://127.0.0.1/private.zip", "https://evil.test/skill.zip", "https://bucket.cos.accelerate.myqcloud.com.evil.test/skill.zip", "http://bucket.cos.accelerate.myqcloud.com/skill.zip"} {
		calls := 0
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: hubRoundTripper(func(r *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				if r.URL.Host != "api.skillhub.cn" || r.URL.Query().Get("slug") != "browser" || r.Header.Get("X-API-Key") != "test-key" {
					t.Fatal("invalid authenticated download")
				}
				return &http.Response{StatusCode: 302, Header: http.Header{"Location": {target}}, Body: io.NopCloser(strings.NewReader(""))}, nil
			}
			if r.Header.Get("X-API-Key") != "" || r.Header.Get("Authorization") != "" {
				t.Fatal("registry key leaked to storage")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(archive))}, nil
		})}
		u, _ := url.Parse("https://api.skillhub.cn/user_team/browser")
		data, source, err := fetchHubSource(context.Background(), client, u)
		if strings.Contains(target, "?sign=") {
			if err != nil || !bytes.Equal(data, archive) || source != u.String() || calls != 2 {
				t.Fatal("signed download failed", err, calls)
			}
		} else if err == nil || calls != 1 {
			t.Fatal("unsafe redirect followed", target, calls)
		}
	}
	if err := os.Chmod(keyFile, 0644); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse("https://api.skillhub.cn/api/v1/download?slug=browser")
	if _, _, err := downloadHub(context.Background(), &http.Client{}, u, MaxUpload, tencentDownloadURL); err == nil {
		t.Fatal("insecure key file accepted")
	}
}

func TestHubImportPreservesSelectedSkillResources(t *testing.T) {
	archive := zipFiles(t, map[string][]byte{"repo-sha/skills/browser/SKILL.md": markdown("browser", "Browse sites"), "repo-sha/skills/browser/scripts/run.py": []byte("print('hello')"), "repo-sha/skills/other/SKILL.md": markdown("other", "Other skill"), "repo-sha/docs/README.md": []byte("not a skill")}, 0600)
	clawArchive := zipFiles(t, map[string][]byte{"SKILL.md": markdown("browser", "Browse sites"), "scripts/run.py": []byte("print('hello')")}, 0600)
	for _, link := range []string{"https://skills.sh/team/repo/browser", "https://clawhub.ai/team/skills/browser"} {
		u, _ := url.Parse(link)
		client := &http.Client{Transport: hubRoundTripper(func(r *http.Request) (*http.Response, error) {
			data := archive
			if u.Host == "clawhub.ai" {
				if r.URL.Host != "clawhub.ai" || r.URL.Path != "/api/v1/download" || r.URL.Query().Get("slug") != "browser" || r.URL.Query().Get("ownerHandle") != "team" {
					t.Fatal("download must identify publisher", r.URL)
				}
				data = clawArchive
			} else if r.URL.String() != "https://api.github.com/repos/team/repo/zipball" {
				t.Fatal("unexpected GitHub archive", r.URL)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: make(http.Header)}, nil
		})}
		data, source, err := fetchHubSource(context.Background(), client, u)
		if err != nil || source != link {
			t.Fatal("hub download failed", link, source, err)
		}
		bundles, err := parseImport("skills.zip", data)
		if err != nil || len(bundles) != 1 || len(bundles[0].Files) != 2 || string(bundles[0].Files["scripts/run.py"]) != "print('hello')" {
			t.Fatal("selected skill resources changed", link, err)
		}
	}
	if _, err := githubSelectedSkill(archive, "missing"); err == nil {
		t.Fatal("missing selection imported neighbours")
	}
}

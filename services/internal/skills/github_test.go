package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestGitHubSourceLinks(t *testing.T) {
	for _, tc := range []struct{ link, archive, directory string }{
		{"https://github.com/team/repo", "https://api.github.com/repos/team/repo/zipball", ""},
		{"https://github.com/team/repo.git/", "https://api.github.com/repos/team/repo/zipball", ""},
		{"https://github.com/team/repo/tree/main", "https://api.github.com/repos/team/repo/zipball/main", ""},
		{"https://github.com/team/repo/tree/v1.2/.agents/skills/test", "https://api.github.com/repos/team/repo/zipball/v1.2", ".agents/skills/test"},
		{"https://github.com/team/repo/tree/feature%2Ftest/skills/test", "https://api.github.com/repos/team/repo/zipball/feature%2Ftest", "skills/test"},
	} {
		u, err := sourceURL(tc.link)
		if err != nil {
			t.Fatal(tc.link, err)
		}
		location, err := githubLocation(u)
		if err != nil || location.archiveURL().String() != tc.archive || location.directory != tc.directory {
			t.Fatal(tc.link, location, err)
		}
	}
	for _, raw := range []string{
		"https://github.com/team", "https://github.com/team/repo/issues", "https://github.com/team/repo/tree",
		"https://github.com/team/repo/tree/main/../secret", "https://github.com/team/repo/tree/main/%2Fsecret",
		"https://github.com/team/repo/tree/%2Fmain/test", "https://github.com/team/repo/tree/main//test",
		"https://github.com/team/repo?token=secret", "https://github.com/team/repo#readme", "https://github.com/team/repo/tree/main/%00",
	} {
		if _, err := sourceURL(raw); err == nil {
			t.Errorf("accepted unsupported link %s", raw)
		}
	}
}

func TestGitHubArchiveFindsOnlySkillSubtrees(t *testing.T) {
	archive := zipFiles(t, map[string][]byte{
		"repo-sha/.github/workflows/ci.yml":             []byte("unrelated"),
		"repo-sha/docs/big.bin":                         make([]byte, MaxFile+1),
		"repo-sha/skills/first/SKILL.md":                markdown("alpha", "first"),
		"repo-sha/skills/first/references/中文.txt":       []byte("附属文件"),
		"repo-sha/.agents/skills/second/SKILL.md":       markdown("beta", "second"),
		"repo-sha/skills/first-neighbour/not-skill.txt": []byte("exclude"),
	}, 0600)
	for _, tc := range []struct {
		directory string
		count     int
	}{{"", 2}, {"skills/first", 1}, {".agents/skills", 1}} {
		content, err := githubSkills(archive, tc.directory)
		if err != nil {
			t.Fatal(tc.directory, err)
		}
		filename := "skills.zip"
		if strings.HasPrefix(string(content), "---") {
			filename = "skill.md"
		}
		bundles, err := parseImport(filename, content)
		if err != nil || len(bundles) != tc.count {
			t.Fatal("lost selected skills", tc.directory, err)
		}
		for _, b := range bundles {
			meta, _ := validateBundle(b)
			if meta.Name == "alpha" && (len(b.Files) != 2 || string(b.Files["references/中文.txt"]) != "附属文件") {
				t.Fatal("changed skill resources")
			}
		}
	}
	if _, err := githubSkills(archive, "skills/firs"); err == nil {
		t.Fatal("directory boundary not enforced")
	}
	root := zipFiles(t, map[string][]byte{"repo-sha/SKILL.md": markdown("root", "root skill"), "repo-sha/.gitignore": []byte("node_modules"), "repo-sha/.github/workflows/test.yml": []byte("ignore")}, 0600)
	if content, err := githubSkills(root, ""); err != nil || !strings.Contains(string(content), "root skill") {
		t.Fatal("root skill with repository metadata rejected", err)
	}
}

func TestGitHubArchiveRejectsUnsafeSkills(t *testing.T) {
	for name, files := range map[string]map[string][]byte{
		"missing":      {"repo-sha/README.md": []byte("not a skill")},
		"traversal":    {"repo-sha/../SKILL.md": markdown("test", "body")},
		"mixed roots":  {"one/SKILL.md": markdown("one", "body"), "two/SKILL.md": markdown("two", "body")},
		"nested":       {"repo-sha/SKILL.md": markdown("one", "body"), "repo-sha/other/SKILL.md": markdown("two", "body")},
		"same name":    {"repo-sha/one/SKILL.md": markdown("same", "body"), "repo-sha/two/SKILL.md": markdown("same", "body")},
		"case clash":   {"repo-sha/a/SKILL.md": markdown("test", "body"), "repo-sha/a/skill.md": []byte("case")},
		"hidden":       {"repo-sha/a/SKILL.md": markdown("test", "body"), "repo-sha/a/.env": []byte("secret")},
		"oversize":     {"repo-sha/a/SKILL.md": markdown("test", "body"), "repo-sha/a/big.bin": make([]byte, MaxFile+1)},
		"invalid yaml": {"repo-sha/a/SKILL.md": []byte("not valid frontmatter")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := githubSkills(zipFiles(t, files, 0600), ""); err == nil {
				t.Fatal("accepted invalid repository")
			}
		})
	}
	if _, err := githubSkills(zipFiles(t, map[string][]byte{"repo-sha/skill/SKILL.md": markdown("test", "body")}, fs.ModeSymlink|0600), ""); err == nil {
		t.Fatal("accepted symlink")
	}
	files := map[string][]byte{"repo-sha/skill/SKILL.md": markdown("test", "body")}
	for i := 0; i < MaxFiles; i++ {
		files["repo-sha/skill/"+strings.Repeat("x", i+1)] = []byte("small")
	}
	if _, err := githubSkills(zipFiles(t, files, 0600), ""); err == nil {
		t.Fatal("accepted too many files")
	}
}

func TestGitHubArchiveDownloadTransport(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	archive := zipFiles(t, map[string][]byte{"repo-sha/skills/test/SKILL.md": markdown("test", "downloaded")}, 0600)
	for _, scenario := range []string{"repository", "directory", "private-redirect", "untrusted-redirect", "credentials-redirect", "rebind", "truncated", "rate-limit"} {
		t.Run(scenario, func(t *testing.T) {
			var hosts []string
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("credentials sent")
				}
				hosts = append(hosts, r.Host)
				if r.Host == "api.github.com" {
					if scenario == "rate-limit" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					redirect := "https://codeload.github.com/team/repo/legacy.zip/main"
					if scenario == "private-redirect" {
						redirect = "https://127.0.0.1/archive.zip"
					} else if scenario == "untrusted-redirect" {
						redirect = "https://other.example/archive.zip"
					} else if scenario == "credentials-redirect" {
						redirect = "https://user:secret@codeload.github.com/archive.zip"
					}
					http.Redirect(w, r, redirect, http.StatusFound)
					return
				}
				if scenario == "truncated" {
					w.Header().Set("Content-Length", "33554433")
					return
				}
				_, _ = w.Write(archive)
			}))
			defer server.Close()
			transport := sourceTransport(func(_ context.Context, _, host string) ([]netip.Addr, error) {
				if scenario == "rebind" && host == "codeload.github.com" {
					return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
				}
				return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
			}, func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != "1.1.1.1:443" {
					t.Errorf("destination was not pinned: %s", address)
				}
				return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
			})
			roots := x509.NewCertPool()
			roots.AddCert(server.Certificate())
			// The fixture certificate is for example.com; production still uses
			// each requested host. Source transport tests cover hostname failures.
			transport.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS12}
			link := "https://github.com/team/repo"
			if scenario == "directory" {
				link += "/tree/main/skills/test"
			}
			body, final, err := fetchSourceWithTransport(context.Background(), link, transport)
			if scenario == "repository" || scenario == "directory" {
				if err != nil || final != link || !strings.Contains(string(body), "downloaded") || len(hosts) != 2 {
					t.Fatal("GitHub archive import failed", final, hosts, err)
				}
			} else if err == nil || body != nil || final != "" {
				t.Fatal("unsafe archive download accepted")
			} else if scenario != "truncated" && len(hosts) != 1 {
				t.Fatal("blocked redirect reached a destination", hosts)
			}
			if files, err := os.ReadDir(temp); err != nil || len(files) != 0 {
				t.Fatal("temporary archive was not cleaned up", files, err)
			}
		})
	}
}

func TestGitHubArchiveLargerThan32MiB(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	for name, content := range map[string][]byte{
		"repo-sha/docs/large.bin":                   make([]byte, (32<<20)+1),
		"repo-sha/skills/test/SKILL.md":             markdown("test", "large repository"),
		"repo-sha/skills/test/references/guide.txt": []byte("selected resource"),
	} {
		entry, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if archive.Len() <= 32<<20 {
		t.Fatal("fixture must exceed the former download limit")
	}
	for _, source := range []string{"repository", "directory", "skills.sh"} {
		for _, length := range []int64{int64(archive.Len()), -1} {
			client := &http.Client{Transport: hubRoundTripper(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != "https://api.github.com/repos/team/repo/zipball" {
					t.Fatal("unexpected download URL", r.URL)
				}
				return &http.Response{StatusCode: 200, ContentLength: length, Body: io.NopCloser(bytes.NewReader(archive.Bytes())), Header: make(http.Header)}, nil
			})}
			var data []byte
			var err error
			if source == "skills.sh" {
				u, _ := url.Parse("https://skills.sh/team/repo/test")
				data, _, err = fetchHubSource(context.Background(), client, u)
			} else {
				location := githubSource{owner: "team", repo: "repo"}
				if source == "directory" {
					location.directory = "skills/test"
				}
				data, err = fetchGitHubSource(context.Background(), client, location, "")
			}
			if err != nil {
				t.Fatal("large repository rejected", source, length, err)
			}
			bundles, err := parseImport("skills.zip", data)
			if err != nil || len(bundles) != 1 || len(bundles[0].Files) != 2 || string(bundles[0].Files["references/guide.txt"]) != "selected resource" {
				t.Fatal("selected skill changed or unrelated files imported", source, err)
			}
			if files, err := os.ReadDir(temp); err != nil || len(files) != 0 {
				t.Fatal("temporary archive was not cleaned up", files, err)
			}
		}
	}
}

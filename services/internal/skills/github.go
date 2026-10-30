package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

var githubOwner = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)
var githubRepo = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)

type githubSource struct {
	owner, repo, ref, directory string
}

func githubLocation(u *url.URL) (githubSource, error) {
	var out githubSource
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(u.EscapedPath(), "/"), "/"), "/")
	for i := range parts {
		value, err := url.PathUnescape(parts[i])
		if err != nil || value == "" || value == "." || value == ".." || strings.ContainsAny(value, "\\\x00") || strings.ContainsFunc(value, unicode.IsControl) {
			return out, invalid("GitHub 链接路径无效")
		}
		parts[i] = value
	}
	if len(parts) < 2 || !githubOwner.MatchString(parts[0]) {
		return out, invalid("请填写完整的 GitHub 仓库链接")
	}
	out.owner, out.repo = parts[0], strings.TrimSuffix(parts[1], ".git")
	if !githubRepo.MatchString(out.repo) || out.repo == "." || out.repo == ".." {
		return out, invalid("GitHub 仓库名称无效")
	}
	if len(parts) == 2 {
		return out, nil
	}
	if len(parts) < 4 || parts[2] != "tree" {
		return out, invalid("请使用 GitHub 仓库、tree 目录或 blob 文件链接")
	}
	out.ref = parts[3]
	if strings.HasPrefix(out.ref, "/") || path.Clean(out.ref) != out.ref {
		return out, invalid("GitHub 分支或版本无效")
	}
	for _, part := range parts[4:] {
		if strings.Contains(part, "/") {
			return out, invalid("GitHub 目录路径无效")
		}
	}
	out.directory = strings.Join(parts[4:], "/")
	return out, nil
}

func (s githubSource) archiveURL() *url.URL {
	u, _ := url.Parse("https://api.github.com/repos/" + s.owner + "/" + s.repo + "/zipball")
	if s.ref != "" {
		u.Path += "/" + s.ref
		u.RawPath = "/repos/" + s.owner + "/" + s.repo + "/zipball/" + url.PathEscape(s.ref)
	}
	return u
}

// Archive redirects keep the same DNS, pinned-IP and TLS checks as file imports,
// and may only reach GitHub's API or archive host. No Git credentials are sent.
func githubArchiveURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || u.Port() != "" && u.Port() != "443" || u.Hostname() != "api.github.com" && u.Hostname() != "codeload.github.com" {
		return nil, invalid("GitHub 归档重定向无效")
	}
	return u, nil
}

func fetchGitHubSource(ctx context.Context, client *http.Client, location githubSource, selected string) ([]byte, error) {
	archive, err := os.CreateTemp("", "hc-github-*.zip")
	if err != nil {
		return nil, invalid("无法暂存 GitHub 仓库归档，请稍后重试")
	}
	defer os.Remove(archive.Name())
	defer archive.Close()
	if _, err = downloadSourceTo(ctx, client, location.archiveURL(), 0, githubArchiveURL, archive); err != nil {
		return nil, err
	}
	info, err := archive.Stat()
	if err != nil {
		return nil, invalid("GitHub 仓库归档无法读取")
	}
	return githubReadArchive(archive, info.Size(), location.directory, selected)
}

// Read only skill subtrees from a GitHub archive. Repository files
// outside those subtrees are never decompressed, extracted or executed.
func githubSkills(data []byte, directory string) ([]byte, error) {
	return githubSkillArchive(data, directory, "")
}

func githubSelectedSkill(data []byte, name string) ([]byte, error) {
	return githubSkillArchive(data, "", name)
}

func githubSkillArchive(data []byte, directory, selected string) ([]byte, error) {
	return githubReadArchive(bytes.NewReader(data), int64(len(data)), directory, selected)
}

func githubReadArchive(reader io.ReaderAt, size int64, directory, selected string) ([]byte, error) {
	z, err := zip.NewReader(reader, size)
	if err != nil {
		return nil, invalid("GitHub 仓库归档无效")
	}
	entries := map[string]*zip.File{}
	rootSet := map[string]bool{}
	wrapper := ""
	for _, entry := range z.File {
		name := strings.TrimSuffix(entry.Name, "/")
		if name == "" || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") || strings.ContainsFunc(name, unicode.IsControl) {
			return nil, invalid("GitHub 归档包含不安全路径")
		}
		prefix, relative, hasPrefix := strings.Cut(name, "/")
		if prefix == "." || prefix == ".." || wrapper != "" && prefix != wrapper || !hasPrefix && !entry.FileInfo().IsDir() {
			return nil, invalid("GitHub 归档根目录无效")
		}
		wrapper = prefix
		if !hasPrefix || entry.FileInfo().IsDir() {
			continue
		}
		if directory != "" && !strings.HasPrefix(relative, directory+"/") {
			continue
		}
		if _, exists := entries[relative]; exists {
			return nil, invalid("GitHub 归档包含重复文件")
		}
		entries[relative] = entry
		if path.Base(relative) == "SKILL.md" {
			if selected != "" {
				if !entry.Mode().IsRegular() {
					continue
				}
				reader, e := entry.Open()
				if e != nil {
					return nil, invalid("GitHub 技能文件无法读取")
				}
				content, e := io.ReadAll(reader)
				reader.Close()
				if e != nil {
					continue
				}
				doc, e := parseDocument(content)
				if e != nil || doc.metadata.Name != selected {
					continue
				}
			}
			root := strings.TrimSuffix(relative, "SKILL.md")
			rootSet[root] = true
		}
	}
	if len(rootSet) == 0 {
		return nil, invalid("所选仓库或目录中未找到 SKILL.md，请检查链接")
	}
	if selected != "" && len(rootSet) != 1 {
		return nil, invalid("仓库中存在多个同名技能，请使用具体技能目录链接")
	}
	roots := make([]string, 0, len(rootSet))
	for root := range rootSet {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	for i := 1; i < len(roots); i++ {
		if strings.HasPrefix(roots[i], roots[i-1]) {
			return nil, invalid("技能目录存在嵌套，请使用具体技能目录链接")
		}
	}
	bundles := make([]Bundle, 0, len(roots))
	names := map[string]bool{}
	for _, root := range roots {
		bundle := Bundle{Files: map[string][]byte{}}
		for name, entry := range entries {
			if !strings.HasPrefix(name, root) {
				continue
			}
			relative := strings.TrimPrefix(name, root)
			// Git hosting metadata is not part of the portable skill bundle.
			if relative == ".gitignore" || relative == ".gitattributes" || strings.HasPrefix(relative, ".github/") {
				continue
			}
			if !safePath(relative) || !entry.Mode().IsRegular() {
				return nil, invalid("技能包含不安全文件，请使用具体技能目录链接")
			}
			reader, err := entry.Open()
			if err != nil {
				return nil, invalid("GitHub 技能文件无法读取")
			}
			content, err := io.ReadAll(reader)
			reader.Close()
			if err != nil {
				return nil, invalid("GitHub 技能读取失败")
			}
			bundle.Files[relative] = content
		}
		meta, err := validateBundle(bundle)
		if err != nil {
			return nil, err
		}
		if names[meta.Name] {
			return nil, invalid("仓库中有重名技能，请使用具体技能目录链接")
		}
		names[meta.Name] = true
		bundles = append(bundles, bundle)
	}
	content, _, err := exportBundles(bundles)
	return content, err
}

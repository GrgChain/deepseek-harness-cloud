package skills

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
	"gopkg.in/yaml.v3"
)

const (
	MaxUpload = 5 << 20
	MaxFile   = 1 << 20
	MaxBundle = 12 << 20
	MaxFiles  = 128
)

var namePattern = regexp.MustCompile(`^[\p{Han}a-z0-9]+(?:-[\p{Han}a-z0-9]+)*$`)
var runtimeNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var idPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)

type Bundle struct {
	Files map[string][]byte `json:"files"`
}
type Metadata struct {
	Name, Description             string
	ModelInvocable, UserInvocable bool
}
type document struct {
	node     *yaml.Node
	body     string
	metadata Metadata
}

func invalid(message string) error { return webapi.Fail(422, "SKILL_INVALID", message) }
func validName(name string) bool {
	return utf8.ValidString(name) && utf8.RuneCountInString(name) > 0 && utf8.RuneCountInString(name) <= 64 && namePattern.MatchString(name)
}

func parseDocument(content []byte) (document, error) {
	var out document
	if !utf8.Valid(content) || bytes.ContainsRune(content, 0) {
		return out, invalid("SKILL.md 须为 UTF-8 文本")
	}
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return out, invalid("SKILL.md 须以 YAML frontmatter 开始")
	}
	end := strings.Index(text[4:], "\n---\n")
	trailing := 5
	if end < 0 && strings.HasSuffix(text, "\n---") {
		end = len(text) - 8
		trailing = 4
	}
	if end < 0 {
		return out, invalid("找不到 frontmatter 的结束分隔符")
	}
	var node yaml.Node
	decoder := yaml.NewDecoder(strings.NewReader(text[4 : 4+end]))
	if decoder.Decode(&node) != nil || len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return out, invalid("YAML frontmatter 格式不正确")
	}
	var extra yaml.Node
	if decoder.Decode(&extra) != io.EOF {
		return out, invalid("frontmatter 只能包含一个 YAML 文档")
	}
	budget := 4096
	var check func(*yaml.Node, int) bool
	check = func(n *yaml.Node, depth int) bool {
		budget--
		if budget < 0 || depth > 24 || n.Kind == yaml.AliasNode || n.Anchor != "" {
			return false
		}
		for _, c := range n.Content {
			if !check(c, depth+1) {
				return false
			}
		}
		return true
	}
	if !check(&node, 0) {
		return out, invalid("frontmatter 不能包含别名或过深的嵌套")
	}
	var fields map[string]any
	if node.Decode(&fields) != nil {
		return out, invalid("frontmatter 包含重复字段或无效值")
	}
	name, ok := fields["name"].(string)
	if !ok || !validName(name) {
		return out, invalid("名称须为 1–64 个中文、小写字母、数字或连字符，连字符不能连续或位于首尾")
	}
	desc, ok := fields["description"].(string)
	if !ok || strings.TrimSpace(desc) == "" || utf8.RuneCountInString(desc) > 2000 || strings.ContainsFunc(desc, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
		return out, invalid("description 须为 1–2000 个字符")
	}
	if value, exists := fields["whenToUse"]; exists {
		text, ok := value.(string)
		if !ok || utf8.RuneCountInString(text) > 4000 {
			return out, invalid("whenToUse 须为不超过 4000 字的文本")
		}
	}
	model, user := true, true
	for key, target := range map[string]*bool{"disable-model-invocation": &model, "user-invocable": &user} {
		if v, exists := fields[key]; exists {
			b, ok := v.(bool)
			if !ok {
				return out, invalid(key + " 须为布尔值")
			}
			if key == "disable-model-invocation" {
				b = !b
			}
			*target = b
		}
	}
	out = document{node: &node, body: text[4+end+trailing:], metadata: Metadata{name, desc, model, user}}
	return out, nil
}

func setPolicy(bundle Bundle, model, user bool) (Bundle, error) {
	doc, err := parseDocument(bundle.Files["SKILL.md"])
	if err != nil {
		return bundle, err
	}
	root := doc.node.Content[0]
	for _, field := range []struct {
		key   string
		value bool
	}{{"disable-model-invocation", !model}, {"user-invocable", user}} {
		key, value := field.key, field.value
		var n yaml.Node
		n.Encode(value)
		found := false
		for i := 0; i < len(root.Content); i += 2 {
			if root.Content[i].Value == key {
				root.Content[i+1] = &n
				found = true
				break
			}
		}
		if !found {
			root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &n)
		}
	}
	header, err := yaml.Marshal(root)
	if err != nil {
		return bundle, err
	}
	files := make(map[string][]byte, len(bundle.Files))
	for k, v := range bundle.Files {
		files[k] = v
	}
	files["SKILL.md"] = []byte("---\n" + string(header) + "---\n" + doc.body)
	return Bundle{files}, nil
}

func newBundle(name, description string) (Bundle, error) {
	header, err := yaml.Marshal(map[string]any{"name": name, "description": description, "disable-model-invocation": true, "user-invocable": true})
	if err != nil {
		return Bundle{}, err
	}
	b := Bundle{map[string][]byte{"SKILL.md": []byte("---\n" + string(header) + "---\n\n在此编写技能说明。\n")}}
	_, err = validateBundle(b)
	return b, err
}

func safePath(name string) bool {
	if name == "" || len(name) > 512 || !utf8.ValidString(name) || strings.ContainsAny(name, "\\\x00") || path.IsAbs(name) || strings.Contains(name, ":") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || len(part) > 128 && !validName(part) || strings.HasPrefix(part, ".") || part == "__MACOSX" {
			return false
		}
		for _, r := range part {
			if r < 32 || r == 127 {
				return false
			}
		}
	}
	return true
}

func validateBundle(bundle Bundle) (Metadata, error) {
	if len(bundle.Files) < 1 {
		return Metadata{}, invalid("每个 Skill 须包含文件")
	}
	paths := map[string]bool{}
	for name := range bundle.Files {
		paths[strings.ToLower(name)] = true
	}
	seen := map[string]bool{}
	for name := range bundle.Files {
		if !safePath(name) || seen[strings.ToLower(name)] {
			return Metadata{}, invalid("文件路径无效或重复：" + name)
		}
		seen[strings.ToLower(name)] = true
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if paths[strings.ToLower(parent)] {
				return Metadata{}, invalid("文件和目录路径冲突：" + parent)
			}
		}
	}
	doc, err := parseDocument(bundle.Files["SKILL.md"])
	return doc.metadata, err
}

func bundleSize(b Bundle) int {
	n := 0
	for _, v := range b.Files {
		n += len(v)
	}
	return n
}
func bundleDigest(b Bundle) string {
	h := sha256.New()
	for _, name := range fileNames(b) {
		fmt.Fprintf(h, "%s\x00", name)
		h.Write(b.Files[name])
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
func fileNames(b Bundle) []string {
	names := make([]string, 0, len(b.Files))
	for name := range b.Files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
func changedFiles(a, b Bundle) []string {
	names := map[string]bool{}
	for n := range a.Files {
		names[n] = true
	}
	for n := range b.Files {
		names[n] = true
	}
	out := []string{}
	for n := range names {
		if !bytes.Equal(a.Files[n], b.Files[n]) {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

func parseImport(filename string, data []byte) ([]Bundle, error) {
	if len(data) == 0 {
		return nil, invalid("上传文件不能为空")
	}
	if strings.HasSuffix(strings.ToLower(filename), ".md") {
		b := Bundle{map[string][]byte{"SKILL.md": data}}
		if _, err := validateBundle(b); err != nil {
			return nil, err
		}
		return []Bundle{b}, nil
	}
	if !strings.HasSuffix(strings.ToLower(filename), ".zip") {
		return nil, invalid("仅支持 .md 或 .zip 文件")
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, invalid("ZIP 文件无法读取")
	}
	files := map[string][]byte{}
	seen := map[string]bool{}
	for _, entry := range z.File {
		name := strings.TrimSuffix(entry.Name, "/")
		if !safePath(name) || seen[strings.ToLower(name)] || entry.Mode()&(^entry.Mode().Perm()) != 0 && !entry.FileInfo().IsDir() {
			return nil, invalid("ZIP 包含重复、不安全路径或特殊文件")
		}
		seen[strings.ToLower(name)] = true
		if entry.FileInfo().IsDir() {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			return nil, invalid("ZIP 文件无法解压")
		}
		content, readErr := io.ReadAll(reader)
		reader.Close()
		if readErr != nil {
			return nil, invalid("ZIP 内容无效")
		}
		files[entry.Name] = content
	}
	// A single wrapper folder may surround one Skill or a multi-Skill export.
	if _, root := files["SKILL.md"]; !root && len(files) > 0 {
		prefix := ""
		common := true
		for name := range files {
			parts := strings.SplitN(name, "/", 2)
			if len(parts) != 2 {
				common = false
				break
			}
			if prefix == "" {
				prefix = parts[0]
			} else if prefix != parts[0] {
				common = false
				break
			}
		}
		if common {
			stripped := map[string][]byte{}
			for name, data := range files {
				stripped[strings.TrimPrefix(name, prefix+"/")] = data
			}
			files = stripped
		}
	}
	if _, root := files["SKILL.md"]; root {
		for name := range files {
			if name != "SKILL.md" && strings.HasSuffix(name, "/SKILL.md") {
				return nil, invalid("根目录 Skill 不能与其他 Skill 混合打包")
			}
		}
		b := Bundle{files}
		_, err = validateBundle(b)
		return []Bundle{b}, err
	}
	groups := map[string]map[string][]byte{}
	for name, data := range files {
		parts := strings.SplitN(name, "/", 2)
		if len(parts) != 2 || !validName(parts[0]) {
			return nil, invalid("ZIP 每个 Skill 须位于一层独立目录")
		}
		if groups[parts[0]] == nil {
			groups[parts[0]] = map[string][]byte{}
		}
		groups[parts[0]][parts[1]] = data
	}
	if len(groups) == 0 {
		return nil, invalid("ZIP 中找不到 SKILL.md")
	}
	out := []Bundle{}
	for name, files := range groups {
		b := Bundle{files}
		meta, err := validateBundle(b)
		if err != nil {
			return nil, err
		}
		if meta.Name != name {
			return nil, invalid("Skill 名称须与目录名称一致：" + name)
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := validateBundle(out[i])
		b, _ := validateBundle(out[j])
		return a.Name < b.Name
	})
	return out, nil
}

func exportBundles(bundles []Bundle) ([]byte, string, error) {
	if len(bundles) == 1 && len(bundles[0].Files) == 1 {
		m, e := validateBundle(bundles[0])
		return bundles[0].Files["SKILL.md"], m.Name + ".md", e
	}
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for _, b := range bundles {
		meta, err := validateBundle(b)
		if err != nil {
			return nil, "", err
		}
		for _, name := range fileNames(b) {
			entry, err := w.Create(meta.Name + "/" + name)
			if err != nil {
				return nil, "", err
			}
			if _, err = entry.Write(b.Files[name]); err != nil {
				return nil, "", err
			}
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	filename := "skills-export.zip"
	if len(bundles) == 1 {
		m, _ := validateBundle(bundles[0])
		filename = m.Name + ".zip"
	}
	return out.Bytes(), filename, nil
}

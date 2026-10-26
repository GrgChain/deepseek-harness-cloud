package skills

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"net/netip"
	"strings"
	"testing"
)

func markdown(name, body string) []byte {
	return []byte("---\nname: " + name + "\ndescription: 技能说明\nwhenToUse: 需要整理时\n---\n" + body)
}
func zipFiles(t *testing.T, files map[string][]byte, mode fs.FileMode) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, body := range files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(mode)
		f, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func TestPolicyAndArchiveRoundTrip(t *testing.T) {
	original := Bundle{map[string][]byte{"SKILL.md": markdown("report", "\n# 正文\n保留说明。\n"), "references/中文.txt": []byte("资源"), "scripts/run.sh": []byte("echo example")}}
	changed, err := setPolicy(original, false, false)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := parseDocument(changed.Files["SKILL.md"])
	if err != nil || doc.metadata.ModelInvocable || doc.metadata.UserInvocable || !strings.Contains(string(changed.Files["SKILL.md"]), "whenToUse: 需要整理时") || doc.body != "\n# 正文\n保留说明。\n" {
		t.Fatalf("lost policy or body: %v %#v", err, doc)
	}
	for _, bundles := range [][]Bundle{{changed}, {changed, {Files: map[string][]byte{"SKILL.md": markdown("another", "other")}}}} {
		raw, name, err := exportBundles(bundles)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parseImport(name, raw)
		if err != nil || len(parsed) != len(bundles) {
			t.Fatal("round trip", err)
		}
		if bundleDigest(parsed[0]) != bundleDigest(bundles[0]) { // Multi exports are sorted by Skill name.
			if len(parsed) != 2 || bundleDigest(parsed[1]) != bundleDigest(bundles[0]) {
				t.Fatal("lost resources")
			}
		}
	}
	if string(original.Files["SKILL.md"]) != string(markdown("report", "\n# 正文\n保留说明。\n")) {
		t.Fatal("mutated original")
	}
}
func TestRejectUnsafeImports(t *testing.T) {
	cases := map[string][]byte{
		"traversal":                zipFiles(t, map[string][]byte{"../SKILL.md": markdown("test", "body")}, 0600),
		"symlink":                  zipFiles(t, map[string][]byte{"SKILL.md": markdown("test", "body")}, fs.ModeSymlink|0600),
		"case collision":           zipFiles(t, map[string][]byte{"SKILL.md": markdown("test", "body"), "skill.md": []byte("other")}, 0600),
		"file directory collision": zipFiles(t, map[string][]byte{"SKILL.md": markdown("test", "body"), "Refs": []byte("file"), "refs/a.txt": []byte("child")}, 0600),
		"zip bomb":                 zipFiles(t, map[string][]byte{"SKILL.md": markdown("test", "body"), "big.txt": bytes.Repeat([]byte("x"), MaxFile+1)}, 0600),
		"hidden":                   zipFiles(t, map[string][]byte{"SKILL.md": markdown("test", "body"), ".env": []byte("secret")}, 0600),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseImport("skills.zip", data); err == nil {
				t.Fatal("accepted unsafe archive")
			}
		})
	}
	for _, header := range []string{"name: test\nname: other\ndescription: text", "name: &n test\ndescription: *n", "name: test\ndescription: text\nuser-invocable: yes", "name: test\ndescription: text\nwhenToUse: 42", "name: test\ndescription: \"\\0\""} {
		if _, err := parseImport("skill.md", []byte("---\n"+header+"\n---\nbody")); err == nil {
			t.Fatalf("accepted %s", header)
		}
	}
	if _, err := parseImport("skill.md", append(markdown("test", ""), 0xff)); err == nil {
		t.Fatal("accepted non UTF-8")
	}
}
func TestPublicSourceValidation(t *testing.T) {
	for _, raw := range []string{"http://example.com/skill.md", "https://example.com:8443/skill.zip", "https://user:pass@example.com/skill.md", "https://example.com/skill.md?token=secret", "https://127.0.0.1/skill.md", "https://[::1]/skill.md", "https://169.254.169.254/skill.md", "https://example.com/repo"} {
		if _, err := sourceURL(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	u, err := sourceURL("https://github.com/team/repo/blob/main/skills/test/SKILL.md")
	if err != nil || u.String() != "https://raw.githubusercontent.com/team/repo/main/skills/test/SKILL.md" {
		t.Fatal(u, err)
	}
	for _, ip := range []string{"0.0.0.0", "127.0.0.1", "10.0.0.1", "100.64.0.1", "192.0.2.1", "198.18.0.1", "224.0.0.1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "2001:db8::1"} {
		if publicAddress(netip.MustParseAddr(ip)) {
			t.Errorf("accepted reserved IP %s", ip)
		}
	}
	for _, ip := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		if !publicAddress(netip.MustParseAddr(ip)) {
			t.Errorf("rejected public IP %s", ip)
		}
	}
}

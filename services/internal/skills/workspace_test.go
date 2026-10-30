package skills

import (
	"bytes"
	"strings"
	"testing"
)

func TestRuntimeWorkspaceSkills(t *testing.T) {
	const id = "b3e2b3e2-1111-4111-8111-111111111111"
	const otherID = "b3e2b3e2-2222-4222-8222-222222222222"
	for _, displayField := range []string{"", "display-name: 旧名称\n"} {
		header := strings.Replace(string(markdown("财报分析-v2", "\n# 中文正文\n保留指令。\n")), "whenToUse:", displayField+"whenToUse:", 1)
		original := Bundle{map[string][]byte{"SKILL.md": []byte(header), "references/中文.txt": []byte("保留资源")}}
		enabled, err := setPolicy(original, true, true)
		if err != nil {
			t.Fatal(err)
		}
		english := Bundle{map[string][]byte{"SKILL.md": markdown("meeting-notes", "English instructions")}}
		items := []WorkspaceSkill{{Selection{id, 3}, "财报分析-v2", enabled}, {Selection{otherID, 1}, "meeting-notes", english}}
		before := append([]byte(nil), enabled.Files["SKILL.md"]...)
		runtime, err := RuntimeWorkspaceSkills(items)
		if err != nil {
			t.Fatal(err)
		}
		if err := ValidateWorkspaceSkills(runtime); err != nil {
			t.Fatal(err)
		}
		if runtime[0].Name != "hc-skill-"+id || runtime[1].Name != "meeting-notes" {
			t.Fatal("unstable runtime names")
		}
		doc, err := parseDocument(runtime[0].Bundle.Files["SKILL.md"])
		if err != nil || doc.body != "\n# 中文正文\n保留指令。\n" || !doc.metadata.ModelInvocable || !doc.metadata.UserInvocable {
			t.Fatal("lost policy or body", err)
		}
		var fields map[string]any
		if err := doc.node.Decode(&fields); err != nil {
			t.Fatal(err)
		}
		if fields["display-name"] != "财报分析-v2" || fields["whenToUse"] != "需要整理时" || string(runtime[0].Bundle.Files["references/中文.txt"]) != "保留资源" {
			t.Fatal("lost metadata or resources")
		}
		if !bytes.Equal(runtime[1].Bundle.Files["SKILL.md"], english.Files["SKILL.md"]) {
			t.Fatal("changed ASCII skill")
		}
		clear(runtime[0].Bundle.Files["SKILL.md"])
		if !bytes.Equal(items[0].Bundle.Files["SKILL.md"], before) || items[0].Name != "财报分析-v2" {
			t.Fatal("mutated library snapshot")
		}
	}
	chinese := WorkspaceSkill{Selection{id, 1}, "中文", Bundle{map[string][]byte{"SKILL.md": markdown("中文", "正文")}}}
	collision := WorkspaceSkill{Selection{otherID, 1}, "hc-skill-" + id, Bundle{map[string][]byte{"SKILL.md": markdown("hc-skill-"+id, "正文")}}}
	if _, err := RuntimeWorkspaceSkills([]WorkspaceSkill{chinese, collision}); err == nil {
		t.Fatal("accepted alias collision")
	}
	chinese.Name = "篡改名称"
	if _, err := RuntimeWorkspaceSkills([]WorkspaceSkill{chinese}); err == nil {
		t.Fatal("accepted inconsistent snapshot")
	}
}

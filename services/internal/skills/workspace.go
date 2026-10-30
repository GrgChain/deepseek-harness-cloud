package skills

import (
	"context"
	"errors"

	"gopkg.in/yaml.v3"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/security"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/webapi"
)

type Selection struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}

type WorkspaceSkill struct {
	Selection
	Name   string `json:"name"`
	Bundle Bundle `json:"bundle"`
}

type workspaceSnapshot struct {
	Skills []WorkspaceSkill `json:"skills"`
}

func ValidSelections(selected []Selection) bool {
	seen := map[string]bool{}
	for _, v := range selected {
		if !idPattern.MatchString(v.ID) || v.Version < 1 || seen[v.ID] {
			return false
		}
		seen[v.ID] = true
	}
	return true
}

// SnapshotWorkspace runs in the instance acceptance transaction. Concurrent edits
// share the library lock; no content or references depend on the mutable library
// after acceptance. Idempotent replays never need to reopen the source skills.
func (s *Service) SnapshotWorkspace(ctx context.Context, tx pgx.Tx, p identity.Principal, instance string, selected []Selection) error {
	if len(selected) == 0 {
		return nil
	}
	if !idPattern.MatchString(instance) || !ValidSelections(selected) {
		return invalid("所选技能无效")
	}
	if err := identity.RequirePermission(ctx, tx, p, "skills:read"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "skills:"+p.OrganizationID+":"+p.UserID); err != nil {
		return err
	}
	items := make([]WorkspaceSkill, 0, len(selected))
	defer func() { DestroyWorkspaceSkills(items) }()
	for _, selection := range selected {
		v, err := load(ctx, tx, p, selection.ID)
		if err != nil {
			return err
		}
		if v.Version != selection.Version {
			return conflict()
		}
		b, err := s.open(p, v)
		if err != nil {
			return err
		}
		// Selecting a skill opts into using it in this workspace. Library imports
		// may have both invocation surfaces disabled; keep the library unchanged
		// and enable the private copy for the native catalog and skill tool.
		enabled, err := setPolicy(b, true, true)
		if err != nil {
			DestroyWorkspaceSkills([]WorkspaceSkill{{Bundle: b}})
			return err
		}
		clear(b.Files["SKILL.md"])
		items = append(items, WorkspaceSkill{selection, v.Name, enabled})
		if _, err = validateBundle(enabled); err != nil {
			return err
		}
	}
	if err := ValidateWorkspaceSkills(items); err != nil {
		return err
	}
	envelope, err := s.seal(p, instance, "workspace-skills", workspaceSnapshot{items})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO workspace_skill_snapshots(instance_id,org_id,owner_id,key_id,nonce,ciphertext) VALUES($1,$2,$3,$4,$5,$6)`, instance, p.OrganizationID, p.UserID, envelope.KeyID, envelope.Nonce, envelope.Ciphertext)
	return err
}

// LoadWorkspaceSkills is used only by the trusted worker on CREATE (including
// recovery). START uses the persistent copies and never overwrites user edits.
func LoadWorkspaceSkills(ctx context.Context, tx pgx.Tx, cipher *security.SecretCipher, environment, org, owner, instance string) ([]WorkspaceSkill, error) {
	var envelope security.Envelope
	err := tx.QueryRow(ctx, `SELECT key_id,nonce,ciphertext FROM workspace_skill_snapshots WHERE instance_id=$1 AND org_id=$2 AND owner_id=$3`, instance, org, owner).Scan(&envelope.KeyID, &envelope.Nonce, &envelope.Ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	raw, err := cipher.Decrypt(envelope, security.Context{Environment: environment, OrganizationID: org, RecordID: instance, Purpose: "workspace-skills"})
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	var snapshot workspaceSnapshot
	if err = webapi.StrictUnmarshal(raw, &snapshot); err == nil {
		err = ValidateWorkspaceSkills(snapshot.Skills)
	}
	if err != nil {
		DestroyWorkspaceSkills(snapshot.Skills)
		return nil, err
	}
	return snapshot.Skills, nil
}

func ValidateWorkspaceSkills(items []WorkspaceSkill) error {
	selected := make([]Selection, 0, len(items))
	names := map[string]bool{}
	runtimeNames := map[string]bool{}
	for _, v := range items {
		selected = append(selected, v.Selection)
		meta, err := validateBundle(v.Bundle)
		if err != nil || meta.Name != v.Name || names[v.Name] {
			return invalid("工作空间技能快照无效")
		}
		names[v.Name] = true
		name := runtimeSkillName(v)
		if runtimeNames[name] {
			return invalid("所选技能的内部标识冲突，请调整技能名称")
		}
		runtimeNames[name] = true
	}
	if !ValidSelections(selected) {
		return invalid("工作空间技能快照无效")
	}
	return nil
}

func runtimeSkillName(item WorkspaceSkill) string {
	if runtimeNamePattern.MatchString(item.Name) {
		return item.Name
	}
	return "hc-skill-" + item.ID
}

// Native DSH requires ASCII invocation names. Keep the frozen library copy and
// display name intact; adapt only the copy sent to its filesystem installer.
// Resource bytes are shared read-only, while translated frontmatter is separate.
func RuntimeWorkspaceSkills(items []WorkspaceSkill) ([]WorkspaceSkill, error) {
	if err := ValidateWorkspaceSkills(items); err != nil {
		return nil, err
	}
	out := make([]WorkspaceSkill, len(items))
	for i, item := range items {
		out[i] = item
		out[i].Name = runtimeSkillName(item)
		if out[i].Name != item.Name {
			doc, err := parseDocument(item.Bundle.Files["SKILL.md"])
			if err != nil {
				return nil, err
			}
			fields := doc.node.Content[0]
			displayName := false
			for j := 0; j < len(fields.Content); j += 2 {
				if fields.Content[j].Value == "name" {
					fields.Content[j+1].Value = out[i].Name
				}
				if fields.Content[j].Value == "display-name" {
					fields.Content[j+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: item.Name}
					displayName = true
				}
			}
			if !displayName {
				fields.Content = append(fields.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "display-name"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: item.Name})
			}
			header, err := yaml.Marshal(fields)
			if err != nil {
				return nil, err
			}
			files := make(map[string][]byte, len(item.Bundle.Files))
			for path, content := range item.Bundle.Files {
				files[path] = content
			}
			files["SKILL.md"] = []byte("---\n" + string(header) + "---\n" + doc.body)
			out[i].Bundle = Bundle{files}
		}
	}
	return out, nil
}

func DestroyWorkspaceSkills(items []WorkspaceSkill) {
	for _, v := range items {
		for _, data := range v.Bundle.Files {
			clear(data)
		}
	}
}

// Keep plaintext skill contents out of accidental diagnostic formatting.
func (v WorkspaceSkill) String() string   { return "[private workspace skill]" }
func (v WorkspaceSkill) GoString() string { return v.String() }

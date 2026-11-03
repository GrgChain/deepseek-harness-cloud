package skills

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jonluo94/deepseek-harness-cloud/services/internal/identity"
)

func (s *Service) Export(ctx context.Context, p identity.Principal, ids []string, previous bool) ([]byte, string, error) {
	if len(ids) < 1 || len(ids) > 50 || previous && len(ids) != 1 {
		return nil, "", invalid("请选择 1–50 项 Skill")
	}
	tx, err := s.begin(ctx, p, false)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback(context.Background())
	bundles := []Bundle{}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nil, "", invalid("导出不能包含重复选择")
		}
		seen[id] = true
		v, err := load(ctx, tx, p, id)
		if err != nil {
			return nil, "", err
		}
		if previous {
			err = tx.QueryRow(ctx, `SELECT key_id,nonce,ciphertext FROM skill_replacements WHERE skill_id=$1`, id).Scan(&v.envelope.KeyID, &v.envelope.Nonce, &v.envelope.Ciphertext)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, "", missing()
			}
			if err != nil {
				return nil, "", err
			}
			raw, err := s.cipher.Decrypt(v.envelope, s.aad(p, id, "skill-bundle"))
			if err != nil {
				return nil, "", err
			}
			var b Bundle
			err = json.Unmarshal(raw, &b)
			clear(raw)
			if err != nil {
				return nil, "", err
			}
			bundles = append(bundles, b)
		} else {
			b, err := s.open(p, v)
			if err != nil {
				return nil, "", err
			}
			bundles = append(bundles, b)
		}
	}
	data, name, err := exportBundles(bundles)
	if err != nil {
		return nil, "", err
	}
	if previous {
		name = "previous-" + name
	}
	return data, name, tx.Commit(ctx)
}

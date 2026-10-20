package modelgateway

import "context"

type accountModel struct {
	ID              string   `json:"id"`
	Object          string   `json:"object"`
	Name            string   `json:"name"`
	ContextWindow   int64    `json:"context_window"`
	MaxOutputTokens int64    `json:"max_output_tokens"`
	ResponseModels  []string `json:"response_models"`
}

func (s *Server) accountModels(ctx context.Context, hash []byte, anchor Route) ([]accountModel, error) {
	rows, err := s.pool.Query(ctx, `SELECT r.alias FROM user_model_connections base
 JOIN user_model_connections h ON h.provider_account_id=base.provider_account_id AND h.org_id=base.org_id AND h.owner_id=base.owner_id
 JOIN model_gateway_routes r ON r.connection_id=h.id AND r.org_id=h.org_id AND r.owner_id=h.owner_id
 WHERE base.id=$1 AND base.org_id=$2 AND base.owner_id=$3 AND h.enabled AND h.archived_at IS NULL AND NOT h.provider_retired AND r.enabled AND (r.connection_version=h.current_version OR r.id=$4)
 ORDER BY h.created_at,h.id,r.connection_version DESC LIMIT 21`, anchor.Connection, anchor.Org, anchor.Owner, anchor.ID)
	if err != nil {
		return nil, err
	}
	var aliases []string
	for rows.Next() {
		var alias string
		if err = rows.Scan(&alias); err != nil {
			rows.Close()
			return nil, err
		}
		aliases = append(aliases, alias)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	models := []accountModel{}
	for _, alias := range aliases {
		route, key, e := s.authorizeRoute(ctx, hash, alias)
		clear(key)
		if e != nil {
			continue
		}
		models = append(models, accountModel{ID: route.Alias, Object: "model", Name: route.Name, ContextWindow: route.ContextWindow, MaxOutputTokens: route.MaxOutputTokens, ResponseModels: route.ResponseModels})
	}
	return models, nil
}

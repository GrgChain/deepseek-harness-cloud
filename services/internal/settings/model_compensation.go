package settings

import "context"

// Scan in stable bounded pages so one large owner cannot starve revocation
// for others. Local admission already rejects inactive owners/catalog entries.
func (a *ModelSync) compensate(ctx context.Context) error {
	rows, err := a.service.pool.Query(ctx, `SELECT h.id::text FROM user_model_connections h WHERE h.id>COALESCE(NULLIF($1,'')::uuid,'00000000-0000-0000-0000-000000000000'::uuid) AND EXISTS(
 SELECT 1 FROM user_model_connection_versions v JOIN model_catalog_entries e ON e.id=v.catalog_entry_id AND e.org_id=v.org_id JOIN model_providers p ON p.id=e.provider_id
 JOIN users u ON u.id=v.owner_id JOIN memberships m ON m.org_id=v.org_id AND m.user_id=v.owner_id JOIN organizations o ON o.id=v.org_id
 WHERE v.connection_id=h.id AND v.sync_status IN ('PENDING_SYNC','SYNCING','UNCERTAIN','SYNCED','FAILED') AND (u.status<>'ACTIVE' OR m.status<>'ACTIVE' OR o.status<>'ACTIVE' OR NOT e.enabled OR NOT p.enabled)) ORDER BY h.id LIMIT 10`, a.cursor)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = a.service.pool.Exec(ctx, `SELECT public.hc_reconcile_personal_model($1)`, id); err != nil {
			return err
		}
		a.cursor = id
	}
	if len(ids) < 10 {
		a.cursor = ""
	}
	return nil
}

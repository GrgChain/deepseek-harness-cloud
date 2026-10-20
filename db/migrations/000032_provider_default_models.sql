-- Defaults belong to an owner-private provider account. Store the stable model
-- identity, so credential rotation uses its current version on the next create.
ALTER TABLE personal_provider_accounts ADD COLUMN default_model_id uuid;
ALTER TABLE personal_provider_accounts ADD CONSTRAINT provider_default_owner_fk
 FOREIGN KEY(org_id,owner_id,default_model_id) REFERENCES user_model_connections(org_id,owner_id,id);
UPDATE personal_provider_accounts g SET default_model_id=COALESCE(
 (SELECT h.id FROM user_model_defaults d JOIN user_model_connections h ON h.id=d.connection_id
  WHERE d.org_id=g.org_id AND d.owner_id=g.owner_id AND h.provider_account_id=g.id AND h.archived_at IS NULL AND h.enabled AND NOT h.provider_retired),
 (SELECT h.id FROM user_model_connections h WHERE h.provider_account_id=g.id AND h.org_id=g.org_id AND h.owner_id=g.owner_id
  AND h.archived_at IS NULL AND h.enabled AND NOT h.provider_retired ORDER BY h.created_at,h.id LIMIT 1)),
 version=version+1,updated_at=clock_timestamp() WHERE g.status='ACTIVE';

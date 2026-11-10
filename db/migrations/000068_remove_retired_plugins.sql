-- Remove retired defaults and their personal presentation overrides.
-- Independently added personal entries and offline archives are preserved.
DELETE FROM personal_plugins
WHERE default_id IN (
 SELECT id FROM plugin_catalog_defaults
 WHERE package_name IN ('@linxin666/dsh-ssh','dshmarket')
);

DELETE FROM plugin_catalog_defaults
WHERE package_name IN ('@linxin666/dsh-ssh','dshmarket');

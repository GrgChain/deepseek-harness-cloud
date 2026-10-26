ALTER TABLE mcp_connectors
 ADD COLUMN endpoint_url text NOT NULL DEFAULT '' CHECK(octet_length(endpoint_url)<=1000),
 ADD COLUMN catalog_id text NOT NULL DEFAULT '' CHECK(length(catalog_id)<=64);

-- Resource sizes are chosen from approved immutable tuples, independently of
-- model credentials. Existing instance snapshots and quotas are unchanged.
DROP INDEX approved_specs_single_enabled;
CREATE INDEX approved_specs_enabled ON approved_specs(cpu_millicores,memory_bytes,storage_bytes) WHERE enabled;
INSERT INTO approved_specs(id,cpu_millicores,memory_bytes,storage_bytes,enabled)
SELECT gen_random_uuid(),cpu,memory,10737418240,true
FROM (VALUES(1000::bigint),(2000::bigint)) cpus(cpu)
CROSS JOIN (VALUES(2147483648::bigint),(4294967296::bigint)) memories(memory)
WHERE EXISTS(SELECT 1 FROM approved_specs WHERE enabled AND cpu_millicores=2000 AND memory_bytes=4294967296 AND storage_bytes=10737418240)
AND NOT EXISTS(SELECT 1 FROM approved_specs WHERE enabled AND cpu_millicores=cpu AND memory_bytes=memory AND storage_bytes=10737418240);

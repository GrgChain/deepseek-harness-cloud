-- Expand compute choices for each already approved storage capacity.
-- Existing snapshots, defaults and user/system quotas remain unchanged.
INSERT INTO approved_specs(id,cpu_millicores,memory_bytes,storage_bytes,enabled)
SELECT gen_random_uuid(),cpu,memory,storage,true
FROM (VALUES(1000::bigint),(2000::bigint),(4000::bigint),(8000::bigint),(16000::bigint)) cpus(cpu)
CROSS JOIN (VALUES(2147483648::bigint),(4294967296::bigint),(8589934592::bigint),(17179869184::bigint),(34359738368::bigint),(68719476736::bigint)) memories(memory)
CROSS JOIN (
    SELECT DISTINCT storage_bytes AS storage FROM approved_specs
    WHERE enabled AND cpu_millicores IN (1000,2000) AND memory_bytes IN (2147483648,4294967296)
      AND storage_bytes BETWEEN 1073741824 AND 107374182400 AND storage_bytes%1073741824=0
) capacities
WHERE NOT EXISTS(SELECT 1 FROM approved_specs WHERE enabled AND cpu_millicores=cpu AND memory_bytes=memory AND storage_bytes=storage);

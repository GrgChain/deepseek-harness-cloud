-- The role editor exposes the complete permission catalog for assignment.
UPDATE permissions
SET assignable = true
WHERE id IN (
    'users:read', 'users:manage', 'roles:read', 'roles:manage',
    'instances:read', 'instances:create', 'instances:start', 'instances:stop',
    'instances:delete', 'instances:configure', 'instances:access', 'instances:retry',
    'configuration:read', 'personal-model:read', 'personal-model:write',
    'personal-model:validate', 'personal-model:revoke', 'models:catalog:manage',
    'model:write', 'model:validate', 'model:revoke', 'defaults:write',
    'audit:read', 'audit:organization'
);

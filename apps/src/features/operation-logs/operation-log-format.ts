import type { OperationLog } from '../../api/resources';

export const operationLogActions: Record<string, string> = {
 LOGIN: '用户登录', LOGOUT: '用户退出', USER_CREATE: '创建用户', USER_UPDATE: '修改用户', USER_ROLE: '调整用户角色', USER_DISABLE: '停用用户', USER_ENABLE: '启用用户', USER_ARCHIVE: '删除用户', USER_QUOTA_UPDATE: '调整资源配额', USER_IDENTITY_CONFIRM: '确认用户身份',
 USER_PASSWORD_RESET: '重置密码', USER_PASSWORD_RESET_REQUESTED: '申请重置密码', USER_PASSWORD_RESET_RETRIED: '重试密码重置', USER_PASSWORD_RESET_UNCERTAIN: '密码重置待确认',
 ROLE_CREATE: '创建角色', ROLE_UPDATE: '修改角色权限', ROLE_DELETE: '删除角色',
 INSTANCE_CREATE: '创建工作空间', INSTANCE_START: '启动工作空间', INSTANCE_STOP: '停止工作空间', INSTANCE_DELETE: '删除工作空间', INSTANCE_CONFIGURE: '更新工作空间模型', INSTANCE_ACCESS: '进入工作空间', INSTANCE_ACCESS_TICKET: '授权进入工作空间', OPERATION_RETRY: '重试工作空间操作',
 PERSONAL_PROVIDER_ACCOUNT_CREATED: '添加模型提供商账号', PERSONAL_PROVIDER_ACCOUNT_UPDATED: '修改模型提供商账号', PERSONAL_PROVIDER_ACCOUNT_DELETE_REQUESTED: '删除模型提供商账号',
 PERSONAL_MODEL_CREATED: '添加模型', PERSONAL_MODEL_UPDATED: '修改模型', PERSONAL_MODEL_VALIDATED: '校验模型', PERSONAL_MODEL_REVOKED: '停用模型', PERSONAL_MODEL_ARCHIVED: '移除模型', PERSONAL_MODEL_DEFAULT_SET: '设置默认模型', PERSONAL_MODEL_DEFAULT_CLEARED: '取消默认模型', PERSONAL_MODEL_GATEWAY_CHECK: '检查模型连接', PERSONAL_MODEL_UPSERT_SYNCED: '同步模型', PERSONAL_MODEL_REVOKE_SYNCED: '清理模型连接',
 MODEL_PROBE: '自动校验模型', MODEL_SAVE: '保存模型配置', MODEL_VALIDATE: '校验模型配置', MODEL_REVOKE: '撤销模型配置', MODEL_DIRECTORY_REFRESH: '同步模型目录', MODEL_CATALOG_SAVED: '保存模型目录', DEFAULTS_SAVE: '更新平台配置', ORGANIZATION_QUOTA_UPDATE: '调整系统资源上限',
 BACKUP_CREATE: '创建备份', BACKUP_SET_CREATE: '创建完整备份', STORAGE_BACKUP: '备份存储', STORAGE_RESTORE: '恢复存储', STORAGE_RECLAIM: '回收存储', RUNTIME_APPROVE: '批准运行模板', RUNTIME_UPGRADE: '升级运行模板', RUNTIME_ROLLBACK: '切换运行模板', IDENTITY_APPLY: '更新身份映射', IDENTITY_IMPORT: '导入用户', WORKSPACE_NAMES_MIGRATE: '更新工作空间名称', RESOURCES_INITIALIZE: '初始化资源配置',
};
export const operationLogOutcomes: Record<string, { label: string; color: string }> = { ACCEPTED: { label: '已受理', color: 'blue' }, SUCCEEDED: { label: '成功', color: 'green' }, FAILED: { label: '失败', color: 'red' }, DENIED: { label: '已拒绝', color: 'orange' } };
export const operationLogResourceTypes: Record<string, string> = { user: '用户', role: '角色', instance: '工作空间', provider_account: '模型提供商账号', model: '模型', session: '登录会话', storage: '存储资产', backup: '备份', runtime: '运行模板', resource: '资源' };
export const quotaFields = [
 { key: 'max_instances', label: '实例数量', scale: 1, unit: '个' }, { key: 'cpu_millicores', label: 'CPU', scale: 1000, unit: 'vCPU' }, { key: 'memory_bytes', label: '内存', scale: 2 ** 30, unit: 'GiB' }, { key: 'storage_bytes', label: '持久存储', scale: 2 ** 30, unit: 'GiB' },
];
const record = (value: unknown): Record<string, unknown> => value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
export function operationLogChanges(event: OperationLog) {
 const details = event.details ?? {}; const before = record(details.before), after = record(details.after);
 const rows: { key: string; label: string; before: string; after: string }[] = [];
 for (const field of quotaFields) if (typeof after[field.key] === 'number') rows.push({ key: field.key, label: field.label, before: typeof before[field.key] === 'number' ? `${Number(before[field.key]) / field.scale} ${field.unit}` : '未记录', after: `${Number(after[field.key]) / field.scale} ${field.unit}` });
 const statuses: Record<string, string> = { ACTIVE: '已启用', DISABLED: '已停用', ARCHIVED: '已删除', PENDING_IDENTITY: '身份待确认' };
 if (typeof details.status === 'string') rows.push({ key: 'status', label: '用户状态', before: typeof details.before_status === 'string' ? statuses[details.before_status] ?? details.before_status : '未记录', after: statuses[details.status] ?? details.status });
 if (typeof details.role_code === 'string') rows.push({ key: 'role', label: '角色', before: typeof details.before_role === 'string' ? details.before_role : '未记录', after: details.role_code });
 if (Array.isArray(details.permissions)) rows.push({ key: 'permissions', label: '权限', before: Array.isArray(details.before_permissions) ? details.before_permissions.join('、') || '无' : '未记录', after: details.permissions.join('、') || '无' });
 return rows;
}
export function operationLogSummary(event: OperationLog) {
 const changes = operationLogChanges(event);
 if (changes.length) return changes.map(change => `${change.label} ${change.before} → ${change.after}`).join('；');
 const reason = event.details?.reason_code;
 const reasons: Record<string, string> = { MODEL_AUTH_REJECTED: '模型密钥认证失败', MODEL_RATE_LIMITED: '模型服务请求受限', MODEL_UNAVAILABLE: '模型服务暂不可用', CREDENTIAL_REVOKED: '模型密钥已撤销', HEALTH_TIMEOUT: '健康检查超时', CAPACITY_UNAVAILABLE: '宿主机容量不足', QUOTA_EXCEEDED: '资源额度不足', PINNED_RUNTIME_VERIFIED: '运行模板核验通过' };
 if (typeof reason === 'string' && reason) return reasons[reason] ?? `原因：${reason}`;
 if (typeof event.details?.configuration_version === 'number') return `模型配置版本 v${event.details.configuration_version}`;
 return '';
}

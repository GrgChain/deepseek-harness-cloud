import type { Instance } from '../../api/resources';

export function instanceModelName(instance: Instance) {
  const name = instance.model_display_name || instance.config_snapshot.model;
  return /^hc-[0-9a-f]{32}-v\d+$/.test(name) ? '模型信息暂不可用' : name;
}

export const instanceOperationNames: Record<string, string> = { CREATE: '创建实例', START: '启动实例', STOP: '停止实例', DELETE: '删除实例', CONFIGURE: '更新模型提供商' };
export const instanceOperationStatuses: Record<string, string> = { ACCEPTED: '已受理', RUNNING: '处理中', UNCERTAIN: '等待确认', SUCCEEDED: '已完成', FAILED: '失败' };
export const instanceOperationStages: Record<string, string> = { QUEUED: '等待执行', PREPARING_STORAGE: '准备存储', CREATING_WORKSPACE: '创建工作空间', BUILDING: '构建运行环境', CONFIGURING: '应用模型配置', CHECKING: '检查运行状态', REVOKING_ACCESS: '关闭访问', STOPPING: '停止计算资源', DELETING: '删除计算资源', RECONCILING: '核对执行结果', COMPLETE: '处理结束' };

export function instanceDate(value: string | null | undefined) {
  return value && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '未记录';
}

import { Tag } from 'antd';

export const assetStatuses: Record<string, string> = {
 READY: '可用', RETAINED: '保留中', PROCESSING: '处理中', DELETED: '已回收',
};
const assetColors: Record<string, string> = {
 READY: 'green', RETAINED: 'gold', PROCESSING: 'blue', DELETED: 'default',
};
export const assetProcessingStages: Record<string, string> = {
 PREPARING: '准备中', RESTORING: '恢复中', RECLAIM_PENDING: '回收待确认',
};
const instanceStatuses: Record<string, string> = {
 CREATING: '创建中', STARTING: '启动中', RUNNING: '运行中', STOPPING: '停止中', STOPPED: '已停止', ERROR: '异常', DELETING: '删除中', DELETED: '已删除',
};
const instanceColors: Record<string, string> = {
 CREATING: 'blue', STARTING: 'cyan', RUNNING: 'green', STOPPING: 'gold', STOPPED: 'orange', ERROR: 'red', DELETING: 'volcano', DELETED: 'default',
};

export function AssetStatus({ status }: { status: string }) {
 const displayStatus = Object.hasOwn(assetProcessingStages, status) ? 'PROCESSING' : status;
 return <Tag color={assetColors[displayStatus] ?? 'default'}>{assetStatuses[displayStatus] ?? '待核对'}</Tag>;
}

export function InstanceStatus({ state, ready }: { state: string; ready: boolean }) {
 const color = state === 'RUNNING' && !ready ? 'gold' : instanceColors[state] ?? 'default';
 return <Tag color={color}>{instanceStatuses[state] ?? '待确认'}</Tag>;
}

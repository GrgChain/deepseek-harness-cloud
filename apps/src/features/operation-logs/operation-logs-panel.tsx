import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Collapse, Descriptions, Drawer, Select, Space, Table, Tag, Typography } from 'antd';
import type { CurrentUser } from '../../api/identity';
import { ResourcesClient, type OperationLog } from '../../api/resources';
import { operationLogActions, operationLogChanges, operationLogOutcomes, operationLogResourceTypes, operationLogSummary } from './operation-log-format';
import { DataPagination, usePagePagination } from '../../shared/components/data-pagination';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';
import { ListSearch } from '../../shared/components/list-search';

const client = new ResourcesClient();
const time = (value: string) => new Date(value).toLocaleString('zh-CN', { hour12: false });
const actor = (event: OperationLog) => event.actor_name || (event.actor_id ? `用户 ${event.actor_id.slice(0, 8)}` : '系统');
const resource = (event: OperationLog) => event.resource_name || `${operationLogResourceTypes[event.resource_type] ?? '资源'}${event.resource_id ? ' ' + event.resource_id.slice(0, 8) : ''}`;
function Outcome({ value }: { value: string }) { const outcome = operationLogOutcomes[value]; return <Tag color={outcome?.color}>{outcome?.label ?? value}</Tag>; }

export function OperationLogsPanel({ user }: { user: CurrentUser }) {
 const [filters, setFilters] = useState({ action: '', outcome: '', days: 1, resource_id: '' });
 const [objectQuery,setObjectQuery]=useState('');
 useEffect(() => { const timer = setTimeout(() => { const resource_id = objectQuery.trim(); setFilters(current => current.resource_id === resource_id ? current : { ...current, resource_id }); }, 250); return () => clearTimeout(timer); }, [objectQuery]);
 const [selected, setSelected] = useState<OperationLog>();
 const pagination = usePagePagination(JSON.stringify([user.id, filters]));
 const list = useQuery({ queryKey: ['resources', user.id, 'operation-logs', filters, pagination.page, pagination.pageSize], queryFn: ({ signal }) => client.operationLogs(undefined, signal, pagination.pageSize, pagination.page, { action: filters.action, outcome: filters.outcome, resource_id: filters.resource_id, ...(filters.days !== 1 ? { from: new Date(Date.now() - filters.days * 86400000).toISOString() } : {}) }), retry: false });
 useExpiredSession(list.error);
 const changes = selected ? operationLogChanges(selected) : [];
 const details = selected?.details ?? {};
 return <section className="settings-section operation-log-section">
  <div className="provider-page-heading"><h2>操作日志</h2></div>
  <div className="workspace-toolbar operation-logs-toolbar">
   <div className="operation-log-filters">
   <Select aria-label="操作日志时间范围" value={filters.days} options={[{ value: 1, label: '最近24小时' }, { value: 7, label: '最近7天' }, { value: 30, label: '最近30天' }]} onChange={days => setFilters({ ...filters, days })} />
   <Select aria-label="日志操作类型" value={filters.action} showSearch optionFilterProp="label" options={[{ value: '', label: '全部操作' }, ...Object.entries(operationLogActions).map(([value, label]) => ({ value, label }))]} onChange={action => setFilters({ ...filters, action })} />
   <Select aria-label="日志操作结果" value={filters.outcome} options={[{ value: '', label: '全部结果' }, ...Object.entries(operationLogOutcomes).map(([value, item]) => ({ value, label: item.label }))]} onChange={outcome => setFilters({ ...filters, outcome })} />
   </div>
   <ListSearch label="按操作对象编号筛选" placeholder="操作对象编号" value={objectQuery} onChange={setObjectQuery} onSubmit={() => setFilters(current => ({ ...current, resource_id: objectQuery.trim() }))} onRefresh={() => void list.refetch()} loading={list.isFetching} refreshLabel="刷新操作日志" />
  </div>
  {list.isError && <Alert type="error" showIcon message="操作日志查询失败" description={list.error.message} action={<Button onClick={() => void list.refetch()}>重试</Button>} />}
  <Table<OperationLog> className="data-table operation-log-table" rowKey="id" loading={list.isPending} dataSource={list.data?.items ?? []} pagination={false} scroll={{ x: 1050 }} locale={{ emptyText: '没有符合条件的操作日志' }} columns={[
   { title: '时间', dataIndex: 'timestamp', width: 180, render: time },
   { title: '操作内容', key: 'action', width: 240, render: (_, event) => <div className="operation-log-operation"><strong>{operationLogActions[event.action] ?? event.action}</strong>{operationLogSummary(event) && <span>{operationLogSummary(event)}</span>}</div> },
   { title: '操作者', key: 'actor', width: 140, render: (_, event) => actor(event) },
   { title: '操作对象', key: 'resource', width: 220, render: (_, event) => <div className="operation-log-operation"><span>{resource(event)}</span><small>{operationLogResourceTypes[event.resource_type] ?? '资源'}</small></div> },
   { title: '结果', dataIndex: 'outcome', width: 100, render: value => <Outcome value={value} /> },
   { title: '操作', key: 'details', width: 100, fixed: 'right', render: (_, event) => <Button onClick={() => setSelected(event)}>详情</Button> },
  ]} />
  <DataPagination label="操作日志" pagination={pagination} count={list.data?.items.length ?? 0} total={list.data?.total} loading={list.isFetching} failed={list.isError} />
  <Drawer title="操作日志详情" width={640} open={!!selected} onClose={() => setSelected(undefined)} destroyOnHidden>
   {selected && <>
    <Descriptions bordered column={1} items={[
     { key: 'action', label: '操作', children: operationLogActions[selected.action] ?? selected.action }, { key: 'outcome', label: '结果', children: <Outcome value={selected.outcome} /> }, { key: 'time', label: '发生时间', children: time(selected.timestamp) }, { key: 'actor', label: '操作者', children: actor(selected) }, { key: 'object', label: '操作对象', children: resource(selected) },
    ]} />
    {changes.length > 0 && <div className="operation-log-detail-block"><h3>变更内容</h3><Table dataSource={changes} rowKey="key" pagination={false} size="small" scroll={{ x: 460 }} columns={[{ title: '项目', dataIndex: 'label' }, { title: '变更前', dataIndex: 'before' }, { title: '变更后', dataIndex: 'after' }]} /></div>}
    {operationLogSummary(selected) && changes.length === 0 && <p className="operation-log-detail-summary">{operationLogSummary(selected)}</p>}
    {!changes.length && !operationLogSummary(selected) && <p className="operation-log-detail-summary">此事件未记录额外变更内容。</p>}
    <Collapse className="operation-log-detail-block" items={[{ key: 'references', label: '关联信息', children: <Descriptions column={1} items={[
     { key: 'code', label: '操作代码', children: selected.action }, ...[
      ['记录编号', selected.id], ['请求编号', selected.trace_id], ['操作者编号', selected.actor_id], ['对象编号', selected.resource_id], ['资源所属用户', selected.owner_id], ['操作任务', details.operation_id], ['重试来源', details.retry_of], ['操作版本', details.version], ['上一版本', details.previous_version], ['操作来源', details.operator_ref],
     ].filter(([, value]) => value !== undefined && value !== null && value !== '').map(([label, value]) => ({ key: String(label), label: String(label), children: <Typography.Text copyable className="operation-log-reference">{String(value)}</Typography.Text> })),
    ]} /> }]} />
    {selected.resource_id && <Space className="operation-log-detail-block"><Button onClick={() => { setObjectQuery(selected.resource_id!); setFilters({ ...filters, resource_id: selected.resource_id! }); setSelected(undefined); }}>查看此对象记录</Button></Space>}
   </>}
  </Drawer>
 </section>;
}

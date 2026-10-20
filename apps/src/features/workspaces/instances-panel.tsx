import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Collapse, Descriptions, Empty, Segmented, Space, Spin, Tag } from 'antd';
import { useLocation, useNavigate, useParams } from 'react-router-dom';
import { ArrowLeft, Box, ChevronRight, CloudDownload, Cpu, ExternalLink, HardDrive, MemoryStick, RefreshCw } from 'lucide-react';
import { hasPermission, type CurrentUser } from '../../api/identity';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';
import { ResourcesClient } from '../../api/resources';
import { enterWorkspace, resumeWorkspace } from '../../api/access';
import { RetryOperationButton, InstanceCommands, CreateInstanceButton } from './instance-commands';
import { DataPagination, usePagePagination } from '../../shared/components/data-pagination';
import {ListSearch} from '../../shared/components/list-search';
import { AssetStatus, InstanceStatus } from '../../shared/components/resource-status';
import { instanceDate, instanceModelName, instanceOperationNames, instanceOperationStages, instanceOperationStatuses } from './instance-display';

const resources = new ResourcesClient();
const healthNames: Record<string, string> = { application: '应用', storage: '存储', configuration: '配置', access: '访问', model: '模型' };
const healthLabels: Record<string, string> = { HEALTHY: '健康', UNHEALTHY: '故障', CHECKING: '检查中', UNKNOWN: '待检查' };

export function InstancesPanel({ user, actions }: { user: CurrentUser; actions?: ReactNode }) {
 const [filter, setFilter] = useState('全部'); const [search, setSearch] = useState(''); const [query, setQuery] = useState('');
 useEffect(()=>{const timer=setTimeout(()=>setQuery(search.trim()),250);return()=>clearTimeout(timer);},[search]);
 const navigate = useNavigate();
 const state = ({ '运行中': 'RUNNING', '已停止': 'STOPPED', '异常': 'ERROR' } as Record<string, string>)[filter];
 const pagination = usePagePagination(JSON.stringify([user.id, state, query]));
 const list = useQuery({ queryKey: ['resources', user.id, 'instances', state, query, pagination.page, pagination.pageSize],
  queryFn: ({ signal }) => resources.instances(user, { state, q: query, limit: pagination.pageSize, page: pagination.page }, undefined, signal),
  retry: false, refetchInterval: 15_000 });
 useExpiredSession(list.error);
 const items = list.data?.items ?? [];
 return <section className="instances-section">
  <div className="provider-page-heading workspace-actions"><h2>工作空间实例</h2>{actions}</div>
  <div className="workspace-toolbar">
   <Segmented aria-label="实例状态筛选" options={['全部', '运行中', '已停止', '异常']} value={filter} onChange={value => setFilter(String(value))} />
   <ListSearch label="搜索实例" placeholder="按名称前缀搜索" value={search} onChange={setSearch} onSubmit={()=>setQuery(search.trim())} refreshLabel="刷新实例列表" loading={list.isFetching} onRefresh={()=>void list.refetch()}/>
  </div>
  {list.isError && <Alert style={{ marginBottom: 16 }} type="error" showIcon message="实例查询失败" description={list.error.message} action={<Button onClick={() => void list.refetch()}>重试</Button>} />}
  <Spin spinning={list.isPending}><div className="workspace-list" aria-label="工作空间列表">{items.length ? items.map(item => <article className="workspace-record" key={item.id}>
   <span className="workspace-symbol" aria-hidden="true"><Box size={20} /></span>
   <div className="workspace-record-content">
    <div className="workspace-record-heading"><h3><Button type="link" onClick={() => navigate('/app/instances/' + item.id)}>{item.name}</Button></h3><InstanceStatus state={item.observed_state} ready={item.ready} />{item.observed_state === 'RUNNING' && !item.ready && <span className="workspace-health">健康待确认</span>}</div>
    <div className="workspace-record-meta"><span>{item.config_snapshot.spec.cpu_millicores / 1000} 核 CPU · {item.config_snapshot.spec.memory_bytes / 2 ** 30} GiB 内存 · {item.config_snapshot.spec.storage_bytes / 2 ** 30} GiB 存储</span><span className="workspace-model">{item.model_provider_name && <>{item.model_provider_name} · </>}{instanceModelName(item)}</span></div>
   </div>
   <Button className="workspace-details" aria-label="实例详情" onClick={() => navigate('/app/instances/' + item.id)}>详情<ChevronRight size={14} aria-hidden="true" /></Button>
  </article>) : <div className="workspace-list-empty"><Box size={28} aria-hidden="true" /><Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={list.isError ? '未能获取实例' : query || state ? '没有符合条件的实例' : '暂无工作空间'}>{list.isSuccess && !query && !state && hasPermission(user, 'instances:create') && hasPermission(user, 'instances:start') && <CreateInstanceButton user={user} label="创建第一个实例" variant="default" />}</Empty></div>}</div></Spin>
  <DataPagination label="实例" pagination={pagination} count={items.length} total={list.data?.total} loading={list.isFetching} failed={list.isError} />
 </section>;
}

export function InstanceDetails({ user }: { user: CurrentUser }) {
 const { id = '' } = useParams(); const navigate = useNavigate();
 const location = useLocation(); const resumed = useRef<string | null>(null);
 const query = useQuery({ queryKey: ['resources', user.id, 'instance', id], queryFn: ({ signal }) => resources.details(user, id, signal), retry: false, refetchInterval: query => query.state.data && ['CREATING','STARTING'].includes(query.state.data.observed_state) ? 1000 : 10_000 });
 const instance = query.data;
 const [entering, setEntering] = useState(false); const [entryError, setEntryError] = useState<Error | null>(null);
 const operationID = instance?.active_operation_id ?? instance?.latest_operation_id;
 const operation = useQuery({ queryKey: ['resources', user.id, 'operation', operationID], enabled: !!operationID, queryFn: ({ signal }) => resources.operation(operationID!, signal), retry: false, refetchInterval: 3000 });
 useEffect(() => {
  if (!query.data || query.isError || new URLSearchParams(location.search).get('resume_workspace') !== '1' || resumed.current === id) return;
  resumed.current = id;
  navigate('/app/instances/' + id, {replace:true});
  setEntering(true); setEntryError(null);
  void resumeWorkspace(user, query.data).then(continuing => { if (!continuing) setEntering(false); }).catch(error => { setEntryError(error instanceof Error ? error : new Error('进入工作空间失败')); setEntering(false); });
 }, [query.data, query.isError, location.search, id, user, navigate]);
 useExpiredSession(query.error ?? operation.error ?? entryError);
 const refresh = () => { void query.refetch(); if (operationID) void operation.refetch(); };
 if (query.isPending) return <div className="instance-loading"><Spin aria-label="加载实例" /></div>;
 if (query.isError || !instance) return <Alert type="error" showIcon message="无法获取实例" description={query.error?.message ?? '资源不存在或不可访问'} action={<Space wrap><Button onClick={refresh}>重试</Button><Button onClick={() => navigate('/app/instances')}>返回实例列表</Button></Space>} />;
 if (entering && resumed.current === id) return <div className="workspace-entry-overlay"><section className="workspace-entry-content" role="status" aria-live="polite" aria-labelledby="workspace-entry-title"><p className="workspace-entry-brand">DSH Cloud</p><div className="workspace-entry-indicator" aria-hidden="true"><CloudDownload size={30} /></div><h1 id="workspace-entry-title">正在进入工作空间</h1><p className="workspace-entry-name">{instance.name}</p><p>正在为你连接，准备好后会自动进入。</p><a href={'/app/instances/' + id}>返回实例详情</a></section></div>;
 const spec = instance.config_snapshot.spec;
 const latest = operation.data;
 const active = !!latest && ['ACCEPTED', 'RUNNING', 'UNCERTAIN'].includes(latest.status);
 const failed = latest?.status === 'FAILED';
 const modelRejected = instance.health.model.status === 'UNHEALTHY' && instance.health.model.reason_code === 'MODEL_AUTH_REJECTED';
 const unhealthy = Object.values(instance.health).some(health => health.status === 'UNHEALTHY');
 const enterHint = instance.can_enter ? null : instance.observed_state === 'STOPPED' ? '启动实例后即可进入工作空间。' : instance.observed_state === 'DELETED' ? '实例已删除。' : instance.observed_state === 'RUNNING' ? unhealthy ? '运行检查未通过，可展开查看详情。' : '正在检查工作空间，请稍候。' : instance.observed_state === 'ERROR' ? '请查看最近操作和运行检查，处理后重试。' : '操作完成后可进入工作空间。';
 const reasons: Record<string, string> = { MODEL_AUTH_REJECTED: '模型密钥无效或访问权限不足', CREDENTIAL_REVOKED: '模型凭据已停用', ACCESS_CLOSED: '工作空间访问尚未开启', STORAGE_MISSING: '存储尚未就绪', HEALTH_REQUIRED: '等待检查', HEALTH_EXPIRED: '等待更新检查结果', MODEL_RATE_LIMITED: '模型服务请求受限', MODEL_TRANSPORT_UNAVAILABLE: '无法连接模型服务' };
 return <section className="settings-section instance-detail" aria-label="实例详情">
  <div className="instance-detail-toolbar"><Button type="text" icon={<ArrowLeft size={16} aria-hidden="true" />} onClick={() => navigate('/app/instances')}>返回实例列表</Button><Button icon={<RefreshCw size={16} aria-hidden="true" />} loading={query.isFetching || operation.isFetching} onClick={refresh}>刷新</Button></div>
  <div className="instance-detail-heading"><span className="workspace-symbol" aria-hidden="true"><Box size={24} /></span><div><div className="instance-detail-title"><h2>{instance.name}</h2><InstanceStatus state={instance.observed_state} ready={instance.ready} /></div>{instance.observed_state === 'RUNNING' && <p>{instance.ready ? '工作空间已就绪' : '运行状态待确认'}</p>}</div></div>
  <div className="instance-detail-actions" aria-label="实例操作">
   <Button type="primary" icon={<ExternalLink size={16} aria-hidden="true" />} disabled={!instance.can_enter || !instance.allowed_actions.includes('access')} loading={entering} onClick={() => { setEntering(true); setEntryError(null); void enterWorkspace(user, instance).catch(error => setEntryError(error instanceof Error ? error : new Error('进入工作空间失败'))).finally(() => setEntering(false)); }}>进入工作空间</Button>
   <InstanceCommands user={user} instance={instance} />
  </div>
  {enterHint && <p className="instance-entry-hint">{enterHint}</p>}
  {entryError && <Alert className="instance-detail-feedback" type="error" showIcon message="进入工作空间失败" description={entryError.message} />}
  {modelRejected && <Alert className="instance-detail-feedback" type="error" showIcon message="模型 API 密钥认证失败" description="请检查所用模型提供商账号的密钥，待模型校验通过后再更新实例配置。" />}
  {latest && <section className="instance-operation" aria-label="最近操作"><div className="instance-section-heading"><h3>最近操作</h3><span>{instanceDate(latest.updated_at)}</span></div>
   <div className="instance-operation-summary" role={active ? 'status' : undefined}><strong>{instanceOperationNames[latest.type] ?? '实例操作'}</strong><Tag color={failed ? 'red' : latest.status === 'SUCCEEDED' ? 'green' : latest.status === 'UNCERTAIN' ? 'orange' : 'blue'}>{instanceOperationStatuses[latest.status] ?? '状态待确认'}</Tag>{active && <span>{instanceOperationStages[latest.stage] ?? '等待进度更新'}</span>}</div>
   {failed && latest.error && <Alert className="instance-operation-error" type="error" showIcon message={latest.error.message} />}
   <RetryOperationButton user={user} instance={instance} operation={latest} />
  </section>}
  {!latest && <RetryOperationButton user={user} instance={instance} operation={undefined} />}
  {operation.isError && <Alert className="instance-detail-feedback" type="warning" showIcon message="操作进度暂不可用" action={<Button onClick={() => void operation.refetch()}>重试</Button>} />}
  <section className="instance-detail-section" aria-label="运行资源"><h3>运行资源</h3><dl className="instance-resource-grid">
   <div><dt><Cpu size={16} aria-hidden="true" />CPU</dt><dd>{spec.cpu_millicores / 1000}<span>核</span></dd></div>
   <div><dt><MemoryStick size={16} aria-hidden="true" />内存</dt><dd>{spec.memory_bytes / 2 ** 30}<span>GiB</span></dd></div>
   <div><dt><HardDrive size={16} aria-hidden="true" />存储</dt><dd>{spec.storage_bytes / 2 ** 30}<span>GiB</span></dd></div>
  </dl></section>
  <section className="instance-detail-section" aria-label="模型配置"><div className="instance-section-heading"><h3>模型配置</h3></div><dl className="instance-model-summary">
   <div><dt>模型提供商</dt><dd>{instance.model_provider_name || '未提供'}</dd></div><div><dt>模型</dt><dd>{instanceModelName(instance)}</dd></div>
  </dl></section>
  <Collapse className="instance-detail-disclosure" ghost expandIcon={({ isActive }) => <ChevronRight size={16} aria-hidden="true" style={{ transform: isActive ? 'rotate(90deg)' : undefined }} />} items={[
   { key: 'health', label: '运行检查', children: <div className="instance-health-list">{Object.entries(instance.health).map(([dimension, health]) => <div className="instance-health-row" key={dimension}><div><strong>{healthNames[dimension]}</strong>{health.status !== 'HEALTHY' && <p>{reasons[health.reason_code] ?? (health.status === 'CHECKING' ? '检查正在进行' : '请刷新检查结果或联系管理员')}</p>}</div><Tag color={health.status === 'HEALTHY' ? 'green' : health.status === 'UNHEALTHY' ? 'red' : 'default'}>{healthLabels[health.status]}</Tag></div>)}</div> },
   { key: 'information', label: '实例信息', children: <Descriptions size="small" column={{ xs: 1, sm: 2 }} items={[
    { key: 'created', label: '创建时间', children: instanceDate(instance.created_at) }, { key: 'updated', label: '更新时间', children: instanceDate(instance.updated_at) },
    { key: 'storage', label: '存储状态', children: instance.storage ? <AssetStatus status={instance.storage.status} /> : '尚未准备' }, { key: 'retention', label: '保留期限', children: instance.retention_until ? instanceDate(instance.retention_until) : '按组织保留策略' },
    { key: 'model-version', label: '模型配置版本', children: `v${instance.config_snapshot.model_configuration_version}` }, { key: 'id', label: '实例编号', children: instance.id },
    { key: 'route', label: '模型路由标识', children: instance.config_snapshot.model },
   ]} /> },
  ]} />
 </section>;
}

import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Space, Spin, Tag } from 'antd';
import type { CurrentUser } from '../../api/identity';
import { SettingsClient } from '../../api/settings';
import { RuntimeDefaultsEditor } from './runtime-defaults-editor';
import { OrganizationQuotaPanel } from '../resource-quotas/organization-quota-panel';
import { useTransientNotice } from '../../shared/hooks/use-transient-notice';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';

const client = new SettingsClient();
const statusNames: Record<string, string> = { HEALTHY: '就绪', UNHEALTHY: '未就绪', UNKNOWN: '待确认', CHECKING: '检查中' };
const checkNames: Record<string, string> = { identity: '组织身份', database: '数据库', redis: '缓存与限流', workspace: '运行编排', docker: '容器运行环境', runtime_manifest: '模板运行校验', storage: '持久存储', capacity: '计算容量', organization_quota: '系统配额配置', access: '工作台入口', template: '运行模板', spec: '运行规格', defaults: '默认用户资源配置', maintenance: '维护状态' };
const reasons: Record<string, string> = { CAPACITY_UNAVAILABLE: '当前主机没有足够的空闲计算资源', QUOTA_NOT_CONFIGURED: '组织额度尚未初始化', RUNTIME_NOT_APPROVED: '运行模板尚未完成校验或与当前环境不一致', STORAGE_UNAVAILABLE: '持久存储容量不足或暂不可用', ACCESS_UNAVAILABLE: '工作台入口暂不可用，请联系管理员', WORKSPACE_UNAVAILABLE: '运行编排暂不可用', RUNTIME_NOT_CONFIGURED: '运行环境尚未接入', TEMPLATE_NOT_CONFIGURED: '尚未批准运行模板', SPEC_NOT_CONFIGURED: '尚未批准运行规格', DEFAULTS_NOT_CONFIGURED: '请先初始化默认用户资源配置', DEPENDENCY_UNAVAILABLE: '依赖暂不可用', MAINTENANCE: '维护中' };

export function SettingsPanel({ user }: { user: CurrentUser }) {
  const [notice,setNotice]=useTransientNotice();
  const cache = useQueryClient();
  const prefix = ['settings', user.id];
  const preflight = useQuery({ queryKey: [...prefix, 'platform-checks'], queryFn: ({ signal }) => client.platformChecks(user, signal), refetchInterval: 30_000, retry: false });
  const defaults = useQuery({ queryKey: [...prefix, 'defaults'], queryFn: ({ signal }) => client.defaults(user, signal), retry: false });
  const refresh = () => { void cache.invalidateQueries({ queryKey: prefix }); };
  const failed = [preflight, defaults].find(result => result.isError);
  useExpiredSession(failed?.error);
  const configuration = defaults.data;
  return <>
    {notice&&<Alert style={{marginBottom:20}} type="success" showIcon closable message={notice} onClose={()=>setNotice(null)}/>}
    {failed && <Alert style={{ marginBottom: 20 }} showIcon type="error" message={failed.error?.message ?? '平台配置读取失败'} action={<Button onClick={refresh}>重新加载</Button>} />}
    <section className="settings-section runtime-defaults-section" aria-label="默认用户资源配置">
      <div className="section-top">
        <div className="settings-card-heading">
          <h2>默认用户资源配置</h2>
          <p>设置新用户默认 CPU、内存、存储和实例上限，并设定后续删除数据的保留期限。</p>
        </div>
        <Space wrap>
          <Button onClick={refresh} loading={defaults.isFetching}>刷新</Button>
          {defaults.isSuccess && <RuntimeDefaultsEditor user={user} configuration={configuration} onSaved={value => {
            cache.setQueryData([...prefix, 'defaults'], value);
            void cache.invalidateQueries({ queryKey: prefix });
            void cache.invalidateQueries({ queryKey: ['management', user.id, 'user-quota'] });
            setNotice('默认用户资源配置已保存');
          }} />}
        </Space>
      </div>
      {defaults.isPending ? <Spin aria-label="加载运行配置" /> : configuration ? <div className="runtime-config-groups">
        <section aria-label="资源规格"><h3>工作空间规格</h3><dl className="runtime-config-grid">
          <div><dt>CPU</dt><dd>{configuration.spec.cpu_millicores / 1000} 核</dd></div>
          <div><dt>内存</dt><dd>{configuration.spec.memory_bytes / 2 ** 30} GiB</dd></div>
          <div><dt>持久存储</dt><dd>{configuration.spec.storage_bytes / 2 ** 30} GiB</dd></div>
        </dl></section>
        <section aria-label="实例策略"><h3>实例策略</h3><dl className="runtime-config-grid">
          <div><dt>每用户默认实例上限</dt><dd>{configuration.max_user_instances} 个</dd></div>
          <div><dt>数据保留期限</dt><dd>{configuration.retention_days} 天</dd></div>
        </dl></section>
      </div> : !defaults.isError && <Alert type="info" showIcon message="尚未初始化默认用户资源配置" />}
    </section>
    <section className="settings-section organization-quota-section" aria-label="系统资源上限">
      <OrganizationQuotaPanel user={user} />
    </section>
    <section className="settings-section settings-check-results" aria-label="检查结果">
      <div className="section-top"><div className="settings-card-heading"><h2>系统检查结果</h2><p>检查系统公共服务和运行资源配置。</p></div><Button onClick={() => void preflight.refetch()} loading={preflight.isFetching}>刷新检查结果</Button></div>
      {preflight.isPending ? <Spin aria-label="加载检查结果" /> : preflight.data && <div className="check-list">{preflight.data.checks.filter(check => check.name !== 'model' && check.name !== 'model_adapter').map(check => <div className="check-row" key={check.name}><div><strong>{checkNames[check.name] ?? check.name}</strong><p>{reasons[check.health.reason_code] ?? (check.health.status === 'HEALTHY' ? '检查通过' : '请联系管理员检查配置')}</p></div><Tag color={check.health.status === 'HEALTHY' ? 'green' : check.health.status === 'UNHEALTHY' ? 'red' : check.health.status === 'CHECKING' ? 'blue' : 'default'}>{statusNames[check.health.status]}</Tag></div>)}</div>}
    </section>
  </>;
}

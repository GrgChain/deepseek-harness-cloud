import type { ReactNode } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Progress, Skeleton, Space, Spin, Table, Tabs, Tag } from 'antd';
import { hasPermission, type CurrentUser } from '../../api/identity';
import { SettingsClient } from '../../api/settings';
import { RuntimeDefaultsEditor } from './runtime-defaults-editor';
import { formatQuotaNumber, quotaDimensions } from '../resource-quotas/quota-format';
import { useTransientNotice } from '../../shared/hooks/use-transient-notice';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';

const client = new SettingsClient();
const statusNames: Record<string, string> = {
  HEALTHY: '就绪',
  UNHEALTHY: '未就绪',
  UNKNOWN: '待确认',
  CHECKING: '检查中',
};
const checkNames: Record<string, string> = {
  identity: '组织身份',
  database: '数据库',
  redis: '缓存与限流',
  workspace: '运行编排',
  docker: '容器运行环境',
  runtime_manifest: '模板运行校验',
  storage: '持久存储',
  capacity: '计算容量',
  organization_quota: '系统配额配置',
  access: '工作台入口',
  template: '运行模板',
  spec: '运行规格',
  defaults: '默认资源配置',
  maintenance: '维护状态',
};
const reasons: Record<string, string> = {
  CAPACITY_UNAVAILABLE: '当前主机没有足够的空闲计算资源',
  QUOTA_NOT_CONFIGURED: '组织额度尚未初始化',
  RUNTIME_NOT_APPROVED: '运行模板尚未完成校验或与当前环境不一致',
  STORAGE_UNAVAILABLE: '持久存储容量不足或暂不可用',
  ACCESS_UNAVAILABLE: '工作台入口暂不可用，请联系管理员',
  WORKSPACE_UNAVAILABLE: '运行编排暂不可用',
  RUNTIME_NOT_CONFIGURED: '运行环境尚未接入',
  TEMPLATE_NOT_CONFIGURED: '尚未批准运行模板',
  SPEC_NOT_CONFIGURED: '尚未批准运行规格',
  DEFAULTS_NOT_CONFIGURED: '请先初始化默认资源配置',
  DEPENDENCY_UNAVAILABLE: '依赖暂不可用',
  MAINTENANCE: '维护中',
};

export function SettingsPanel({
  user,
  diagnostics,
}: {
  user: CurrentUser;
  diagnostics: ReactNode;
}) {
  const [notice, setNotice] = useTransientNotice();
  const cache = useQueryClient();
  const prefix = ['settings', user.id];
  const preflight = useQuery({
    queryKey: [...prefix, 'platform-checks'],
    queryFn: ({ signal }) => client.platformChecks(user, signal),
    refetchInterval: 30_000,
    retry: false,
  });
  const defaults = useQuery({
    queryKey: [...prefix, 'defaults'],
    queryFn: ({ signal }) => client.defaults(user, signal),
    retry: false,
  });
  const quotaKey = ['management', user.id, 'organization-quota'];
  const quota = useQuery({
    queryKey: quotaKey,
    queryFn: ({ signal }) => client.organizationQuota(user, signal),
    retry: false,
    refetchOnWindowFocus: false,
  });
  const canEdit = hasPermission(user, 'defaults:write');
  const refresh = () => {
    void cache.invalidateQueries({ queryKey: prefix });
    void cache.invalidateQueries({ queryKey: quotaKey });
  };
  const failed = [preflight, defaults, quota].find((result) => result.isError);
  useExpiredSession(failed?.error);
  const configuration = defaults.data;
  const quotaRows = quota.data
    ? quotaDimensions.map((dimension) => ({
        key: dimension.key,
        label: dimension.label,
        unit: dimension.unit,
        used: quota.data.usage[dimension.key] / dimension.scale,
        limit: quota.data.limits[dimension.key] / dimension.scale,
      }))
    : [];
  return (
    <div className="settings-modules">
      {notice && (
        <Alert type="success" showIcon closable message={notice} onClose={() => setNotice(null)} />
      )}
      <Tabs
        className="settings-tabs"
        aria-label="平台配置模块"
        defaultActiveKey="defaults"
        items={[
          {
            key: 'defaults',
            label: '默认资源',
            children: (
              <>
                {(defaults.isError || quota.isError) && (
                  <Alert
                    className="settings-module-error"
                    showIcon
                    type="error"
                    message={(defaults.error ?? quota.error)?.message ?? '无法读取资源配置'}
                    action={<Button onClick={refresh}>重新加载</Button>}
                  />
                )}
                <section
                  className="settings-section runtime-defaults-section"
                  aria-label="默认资源配置"
                >
                  <div className="section-top">
                    <div className="settings-card-heading">
                      <h2>默认资源配置</h2>
                      <p>
                        设置新用户默认工作空间规格、实例上限与数据保留期限，并统一管控全系统可分配资源总量上限。
                      </p>
                    </div>
                    <Space wrap>
                      <Button onClick={refresh} loading={defaults.isFetching || quota.isFetching}>
                        刷新
                      </Button>
                      {defaults.isSuccess && (
                        <RuntimeDefaultsEditor
                          user={user}
                          configuration={configuration}
                          quota={quota.data}
                          onSaved={({ defaults: savedDefaults, quota: savedQuota }) => {
                            if (savedDefaults) {
                              cache.setQueryData([...prefix, 'defaults'], savedDefaults);
                            }
                            if (savedQuota) {
                              cache.setQueryData(quotaKey, savedQuota);
                            }
                            void cache.invalidateQueries({ queryKey: prefix });
                            void cache.invalidateQueries({ queryKey: quotaKey });
                            void cache.invalidateQueries({
                              queryKey: ['management', user.id, 'user-quota'],
                            });
                            setNotice(
                              savedDefaults && savedQuota
                                ? '默认资源配置与系统资源上限已保存'
                                : savedQuota
                                  ? '系统资源上限已保存'
                                  : '默认资源配置已保存',
                            );
                          }}
                        />
                      )}
                    </Space>
                  </div>
                  {!canEdit && (
                    <Alert
                      type="info"
                      showIcon
                      message="当前账户仅可查看默认资源配置与系统资源上限"
                      style={{ marginBottom: 16 }}
                    />
                  )}
                  <div className="runtime-config-groups">
                    {defaults.isPending ? (
                      <Spin aria-label="加载运行配置" />
                    ) : configuration ? (
                      <>
                        <section aria-label="资源规格">
                          <h3>工作空间规格</h3>
                          <dl className="runtime-config-grid">
                            <div>
                              <dt>CPU</dt>
                              <dd>{configuration.spec.cpu_millicores / 1000} 核</dd>
                            </div>
                            <div>
                              <dt>内存</dt>
                              <dd>{configuration.spec.memory_bytes / 2 ** 30} GiB</dd>
                            </div>
                            <div>
                              <dt>持久存储</dt>
                              <dd>{configuration.spec.storage_bytes / 2 ** 30} GiB</dd>
                            </div>
                          </dl>
                        </section>
                        <section aria-label="实例策略">
                          <h3>实例策略</h3>
                          <dl className="runtime-config-grid">
                            <div>
                              <dt>每用户默认实例上限</dt>
                              <dd>{configuration.max_user_instances} 个</dd>
                            </div>
                            <div>
                              <dt>数据保留期限</dt>
                              <dd>{configuration.retention_days} 天</dd>
                            </div>
                          </dl>
                        </section>
                      </>
                    ) : (
                      !defaults.isError && (
                        <Alert type="info" showIcon message="尚未初始化默认资源配置" />
                      )
                    )}
                    <section className="organization-quota-subsection" aria-label="系统资源上限">
                      <div className="organization-quota-subsection-header">
                        <h3>系统资源上限</h3>
                        <span>
                          系统上限不得低于当前占用、默认单用户实例上限（
                          {configuration?.max_user_instances ??
                            quota.data?.default_user_instances ??
                            '—'}{' '}
                          个）或已分配的用户额度。
                        </span>
                      </div>
                      {quota.isPending ? (
                        <Skeleton active paragraph={{ rows: 4 }} />
                      ) : (
                        quota.data && (
                          <Table
                            className="data-table users-table organization-quota-table"
                            size="middle"
                            rowKey="key"
                            tableLayout="fixed"
                            dataSource={quotaRows}
                            pagination={false}
                            scroll={{ x: 585 }}
                            columns={[
                              { title: '资源', dataIndex: 'label', width: 120 },
                              {
                                title: '当前占用',
                                key: 'usage',
                                width: 150,
                                render: (_, row) => (
                                  <span className="quota-table-value">
                                    <strong>{formatQuotaNumber(row.used)}</strong>
                                    <span> {row.unit}</span>
                                  </span>
                                ),
                              },
                              {
                                title: '系统上限',
                                key: 'limit',
                                width: 150,
                                render: (_, row) => (
                                  <span className="quota-table-value">
                                    <strong>{formatQuotaNumber(row.limit)}</strong>
                                    <span> {row.unit}</span>
                                  </span>
                                ),
                              },
                              {
                                title: '使用率',
                                key: 'progress',
                                width: 165,
                                render: (_, row) => (
                                  <div className="organization-quota-progress">
                                    <Progress
                                      percent={
                                        row.limit === 0
                                          ? row.used === 0
                                            ? 0
                                            : 100
                                          : Math.min(100, (row.used / row.limit) * 100)
                                      }
                                      showInfo={false}
                                      size="small"
                                      status={row.used > row.limit ? 'exception' : 'normal'}
                                    />
                                    <span>
                                      {formatQuotaNumber(
                                        row.limit === 0 ? 0 : (row.used / row.limit) * 100,
                                      )}
                                      %
                                    </span>
                                  </div>
                                ),
                              },
                            ]}
                          />
                        )
                      )}
                    </section>
                  </div>
                </section>
              </>
            ),
          },
          {
            key: 'checks',
            label: '系统检查',
            children: (
              <>
                {diagnostics}
                {preflight.isError && (
                  <Alert
                    className="settings-module-error"
                    showIcon
                    type="error"
                    message={preflight.error.message}
                    action={<Button onClick={refresh}>重新加载</Button>}
                  />
                )}
                <section className="settings-section settings-check-results" aria-label="检查结果">
                  <div className="section-top">
                    <div className="settings-card-heading">
                      <h2>系统检查结果</h2>
                      <p>检查系统公共服务和运行资源配置。</p>
                    </div>
                    <Button onClick={() => void preflight.refetch()} loading={preflight.isFetching}>
                      刷新检查结果
                    </Button>
                  </div>
                  {preflight.isPending ? (
                    <Spin aria-label="加载检查结果" />
                  ) : (
                    preflight.data && (
                      <div className="check-list">
                        {preflight.data.checks
                          .filter(
                            (check) => check.name !== 'model' && check.name !== 'model_adapter',
                          )
                          .map((check) => (
                            <div className="check-row" key={check.name}>
                              <div>
                                <strong>{checkNames[check.name] ?? check.name}</strong>
                                <p>
                                  {reasons[check.health.reason_code] ??
                                    (check.health.status === 'HEALTHY'
                                      ? '检查通过'
                                      : '请联系管理员检查配置')}
                                </p>
                              </div>
                              <Tag
                                color={
                                  check.health.status === 'HEALTHY'
                                    ? 'green'
                                    : check.health.status === 'UNHEALTHY'
                                      ? 'red'
                                      : check.health.status === 'CHECKING'
                                        ? 'blue'
                                        : 'default'
                                }
                              >
                                {statusNames[check.health.status]}
                              </Tag>
                            </div>
                          ))}
                      </div>
                    )
                  )}
                </section>
              </>
            ),
          },
        ]}
      />
    </div>
  );
}

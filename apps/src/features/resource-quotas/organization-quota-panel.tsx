import { useEffect, useId, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Drawer, Form, InputNumber, Progress, Skeleton, Space, Table } from 'antd';
import type { CurrentUser } from '../../api/identity';
import { SettingsClient, type OrganizationQuotaLimits } from '../../api/settings';
import { quotaDimensions, formatQuotaNumber } from './quota-format';
import { useTransientNotice } from '../../shared/hooks/use-transient-notice';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';

const client = new SettingsClient();

export function OrganizationQuotaPanel({ user }: { user: CurrentUser }) {
  const cache = useQueryClient();
  const [form] = Form.useForm<OrganizationQuotaLimits>();
  const formID = useId();
  const [open, setOpen] = useState(false);
  const [notice, setNotice] = useTransientNotice();
  const key = ['management', user.id, 'organization-quota'];
  const quota = useQuery({ queryKey: key, queryFn: ({ signal }) => client.organizationQuota(user, signal), retry: false, refetchOnWindowFocus: false });
  const editable = user.permissions.includes('defaults:write');
  const mutation = useMutation({
    mutationFn: (values: OrganizationQuotaLimits) => {
      if (!quota.data) throw new Error('请先读取组织配额');
      const limits = Object.fromEntries(quotaDimensions.map(d => [d.key, values[d.key] * d.scale])) as unknown as OrganizationQuotaLimits;
      return client.saveOrganizationQuota(user, limits, quota.data.version);
    },
    onSuccess: value => { setOpen(false); cache.setQueryData(key, value); void cache.invalidateQueries({ queryKey: ['management', user.id, 'user-quota'] }); setNotice('系统资源上限已保存'); },
  });
  useEffect(() => {
    if (quota.data) form.setFieldsValue(Object.fromEntries(quotaDimensions.map(d => [d.key, quota.data.limits[d.key] / d.scale])));
  }, [quota.data, form]);
  const error = mutation.error ?? quota.error;
  useExpiredSession(error);
  const reload = () => { mutation.reset(); setNotice(null); void quota.refetch(); };
  const edit = () => {
    mutation.reset();
    if (quota.data) form.setFieldsValue(Object.fromEntries(quotaDimensions.map(d => [d.key, quota.data.limits[d.key] / d.scale])));
    setOpen(true);
  };
  const rows = quota.data ? quotaDimensions.map(dimension => ({
    key: dimension.key,
    label: dimension.label,
    unit: dimension.unit,
    used: quota.data.usage[dimension.key] / dimension.scale,
    limit: quota.data.limits[dimension.key] / dimension.scale,
  })) : [];
  return <div className="organization-quota-panel">
    <div className="section-top">
      <div className="settings-card-heading"><h2>系统资源上限</h2><p>控制全系统可分配的实例、CPU、内存和持久存储总量。</p></div>
      <Space wrap>
        <Button onClick={reload} loading={quota.isFetching} disabled={mutation.isPending}>刷新</Button>
        {editable && <Button type="primary" onClick={edit} disabled={!quota.data || quota.isFetching || mutation.isPending}>修改配置</Button>}
      </Space>
    </div>
    {notice && <Alert type="success" showIcon closable onClose={() => setNotice(null)} message={notice} style={{ marginBottom: 16 }} />}
    {quota.error && <Alert type="error" showIcon message={quota.error.message} style={{ marginBottom: 16 }} />}
    {!editable && <Alert type="info" showIcon message="当前账户仅可查看系统资源上限" style={{ marginBottom: 16 }} />}
    {quota.isPending && <Skeleton active paragraph={{ rows: 4 }} />}
    {quota.isError && !error && <Alert type="error" showIcon message="无法读取系统资源上限" />}
    {quota.data && <>
      <Alert type="info" showIcon message={`默认单用户实例上限：${quota.data.default_user_instances} 个`} description="系统上限不得低于当前占用、默认单用户实例上限或已分配的用户额度。" style={{ marginBottom: 16 }} />
      <Table className="data-table users-table organization-quota-table" size="middle" rowKey="key" tableLayout="fixed" dataSource={rows} pagination={false} scroll={{ x: 585 }} columns={[
        { title: '资源', dataIndex: 'label', width: 120 },
        { title: '当前占用', key: 'usage', width: 150, render: (_, row) => <span className="quota-table-value"><strong>{formatQuotaNumber(row.used)}</strong><span> {row.unit}</span></span> },
        { title: '系统上限', key: 'limit', width: 150, render: (_, row) => <span className="quota-table-value"><strong>{formatQuotaNumber(row.limit)}</strong><span> {row.unit}</span></span> },
        { title: '使用率', key: 'progress', width: 165, render: (_, row) => <div className="organization-quota-progress"><Progress percent={row.limit === 0 ? row.used === 0 ? 0 : 100 : Math.min(100, row.used / row.limit * 100)} showInfo={false} size="small" status={row.used > row.limit ? 'exception' : 'normal'} /><span>{formatQuotaNumber(row.limit === 0 ? 0 : row.used / row.limit * 100)}%</span></div> },
      ]} />
      <Drawer title="修改系统资源上限" width={560} open={open} onClose={() => { if (!mutation.isPending) setOpen(false); }} closable={!mutation.isPending} maskClosable={!mutation.isPending} keyboard={!mutation.isPending} destroyOnHidden footer={<div className="create-instance-footer"><Button disabled={mutation.isPending} onClick={() => setOpen(false)}>取消</Button><Button type="primary" htmlType="submit" form={formID} loading={mutation.isPending} disabled={!quota.data || quota.isFetching || mutation.isError}>保存配置</Button></div>}>
      {mutation.isError && <Alert type="error" showIcon message={mutation.error.message} description="重新读取最新上限与占用后，再确认保存。" action={<Button onClick={reload}>重新读取</Button>} style={{ marginBottom: 16 }} />}
      <Form form={form} id={formID} layout="vertical" className="organization-quota-form" disabled={!editable || quota.isFetching || mutation.isPending} onFinish={values => mutation.mutate(values)}>
        <div className="organization-quota-fields">{quotaDimensions.map(dimension => {
          const used = quota.data!.usage[dimension.key] / dimension.scale;
          return <Form.Item key={dimension.key} name={dimension.key} label={`${dimension.label}上限（${dimension.unit}）`} rules={[{ required: true, message: '请填写系统资源上限' }, { validator: (_, value: number | null | undefined) => {
            if (value == null || !Number.isSafeInteger(value * dimension.scale) || value < 1) return Promise.reject(new Error('请输入大于 0 的有效数值'));
            if (value < used) return Promise.reject(new Error(`不能低于当前占用 ${formatQuotaNumber(used)} ${dimension.unit}`));
            return Promise.resolve();
          } }]}><InputNumber min={dimension.step} step={dimension.step} style={{ width: '100%' }} /></Form.Item>;
        })}</div>
      </Form>
      </Drawer>
    </>}
  </div>;
}

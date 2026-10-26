import { quotaDimensions, formatQuotaNumber } from './quota-format';
import { useEffect } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Form, InputNumber, Modal, Progress, Skeleton, Space, Tag } from 'antd';
import type { CurrentUser } from '../../api/identity';
import { ManagementClient, type ManagedUser, type UserQuotaLimits } from '../../api/management';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';

const client = new ManagementClient();
export function UserQuotaDialog({ user, target, onClose, onSaved }: { user: CurrentUser; target: ManagedUser; onClose: () => void; onSaved: () => void }) {
  const cache = useQueryClient();
  const [form] = Form.useForm<UserQuotaLimits>();
  const key = ['management', user.id, 'user-quota', target.id];
  const query = useQuery({ queryKey: key, queryFn: ({ signal }) => client.userQuota(user, target.id, signal), staleTime: 15_000, retry: false, refetchOnWindowFocus: false });
  const quota = query.data;
  const editable = user.permissions.includes('users:manage') && target.status !== 'ARCHIVED';
  const mutation = useMutation({
    mutationFn: (values: UserQuotaLimits) => {
      if (!quota) throw new Error('请先读取配额');
      const limits = Object.fromEntries(quotaDimensions.map(d => [d.key, values[d.key] * d.scale])) as unknown as UserQuotaLimits;
      return client.saveUserQuota(user, target.id, limits, quota.version);
    },
    onSuccess: result => { cache.setQueryData(key, result); onSaved(); onClose(); },
  });
  useEffect(() => {
    if (quota) form.setFieldsValue(Object.fromEntries(quotaDimensions.map(d => [d.key, quota.limits[d.key] / d.scale])));
  }, [quota, form]);
  const error = mutation.error ?? query.error;
  useExpiredSession(error);
  const reload = () => { mutation.reset(); void query.refetch(); };
  return <Modal open title="资源配额" width={640} style={{ top: 24 }} styles={{ body: { maxHeight: 'calc(100dvh - 200px)', overflowY: 'auto', paddingRight: 4 } }} onCancel={() => { if (!mutation.isPending) onClose(); }} maskClosable={!mutation.isPending} closable={!mutation.isPending} footer={<Space wrap>
    <Button aria-label="重新读取" onClick={reload} loading={query.isFetching} disabled={mutation.isPending}>重新读取</Button>
    <Button onClick={onClose} disabled={mutation.isPending}>取消</Button>
    <Button aria-label="保存配额" type="primary" onClick={() => form.submit()} loading={mutation.isPending} disabled={!editable || !quota || query.isFetching || query.isError || mutation.isError}>保存配额</Button>
  </Space>}>
    <div className="quota-user"><strong>{target.display_name || target.id}</strong><span>{target.email}</span>{quota && <Tag color={quota.configured ? 'blue' : 'default'}>{quota.configured ? '单独分配' : '继承组织默认'}</Tag>}</div>
    <p className="quota-description">用户额度与系统总额度同时生效。额度为使用上限，用户共享系统资源池。</p>
    {error && <Alert type="error" showIcon message={error.message} description="重新读取最新额度与占用后，再确认保存。" style={{ marginBottom: 16 }} />}
    {query.isPending && <Skeleton active paragraph={{ rows: 5 }} />}
    <Form form={form} layout="vertical" disabled={!editable || !quota || query.isFetching || mutation.isPending} onFinish={values => mutation.mutate(values)}>
      {quota && <div className="quota-fields">{quotaDimensions.map(d => {
        const used = quota.usage[d.key] / d.scale;
        const limit = quota.limits[d.key] / d.scale;
        const maximum = quota.organization_limits[d.key] / d.scale;
        return <section className="quota-field" key={d.key} aria-label={d.label}>
          <div className="quota-field-heading"><h3>{d.label}</h3><span>系统上限 {formatQuotaNumber(maximum)} {d.unit}</span></div>
          <div className="quota-usage"><span>已占用 <strong>{formatQuotaNumber(used)}</strong> / {formatQuotaNumber(limit)} {d.unit}</span><Progress aria-label={`${d.label}已占用 ${formatQuotaNumber(used)} / ${formatQuotaNumber(limit)} ${d.unit}`} percent={limit === 0 ? used === 0 ? 0 : 100 : Math.min(100, used / limit * 100)} showInfo={false} size="small" status={used > limit ? 'exception' : 'normal'} /></div>
          <Form.Item name={d.key} label="用户上限" rules={[{ required: true, message: '请填写额度，0 表示禁止新增占用' }, { validator: (_, value: number | null | undefined) => {
            if (value == null) return Promise.resolve();
            if (!Number.isSafeInteger(value * d.scale) || value < 0) return Promise.reject(new Error('请输入非负值，数量须为整数'));
            if (value < used) return Promise.reject(new Error(`不能低于当前占用 ${formatQuotaNumber(used)} ${d.unit}`));
            if (value > maximum) return Promise.reject(new Error('不能高于系统上限'));
            return Promise.resolve();
          } }]}><InputNumber aria-label={`${d.label}上限（${d.unit}）`} min={0} step={d.step} style={{ width: '100%' }} /></Form.Item>
        </section>;
      })}</div>}
    </Form>
    <p className="quota-description">设为 0 可限制该维度新增占用。额度不能低于当前占用。</p>
    {!editable && <Alert showIcon type="info" message={target.status === 'ARCHIVED' ? '已归档用户的配额仅可查看' : '当前账号仅可查看配额'} />}
  </Modal>;
}

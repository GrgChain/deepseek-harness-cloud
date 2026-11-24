import { useEffect, useId, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Alert, Button, Drawer, Form, Input, InputNumber, Select, Spin } from 'antd';
import { hasPermission, type CurrentUser } from '../../api/identity';
import {
  SettingsClient,
  type Defaults,
  type OrganizationQuota,
  type OrganizationQuotaLimits,
} from '../../api/settings';
import { formatQuotaNumber, quotaDimensions } from '../resource-quotas/quota-format';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';

const client = new SettingsClient();
const GiB = 2 ** 30;
interface Fields {
  template: string;
  cpu: number;
  memory: number;
  storage: number;
  max_user_instances: number;
  retention_days: number;
  quota_max_instances?: number;
  quota_cpu_millicores?: number;
  quota_memory_bytes?: number;
  quota_storage_bytes?: number;
}

const quotaFieldNames: Record<keyof OrganizationQuotaLimits, keyof Fields> = {
  max_instances: 'quota_max_instances',
  cpu_millicores: 'quota_cpu_millicores',
  memory_bytes: 'quota_memory_bytes',
  storage_bytes: 'quota_storage_bytes',
};

export function RuntimeDefaultsEditor({
  user,
  configuration,
  quota,
  onSaved,
}: {
  user: CurrentUser;
  configuration: Defaults | null | undefined;
  quota?: OrganizationQuota | null;
  onSaved: (result: { defaults?: Defaults; quota?: OrganizationQuota }) => void;
}) {
  const [open, setOpen] = useState(false);
  const [base, setBase] = useState(configuration);
  const [baseQuota, setBaseQuota] = useState(quota);
  const [form] = Form.useForm<Fields>();
  const formID = useId();
  const canEdit = hasPermission(user, 'defaults:write');
  const templates = useQuery({
    queryKey: ['settings', user.id, 'templates'],
    enabled: open && canEdit,
    queryFn: ({ signal }) => client.templates(user, signal),
    retry: false,
  });
  const items = templates.data?.items ?? [];
  const template = Form.useWatch('template', form);
  const cpu = Form.useWatch('cpu', form);
  const memory = Form.useWatch('memory', form);
  const storage = Form.useWatch('storage', form);
  const candidates = items.filter((item) => item.id + ':' + item.template_version === template);
  const selected = candidates.find(
    (item) =>
      item.spec.cpu_millicores === cpu &&
      item.spec.memory_bytes === memory &&
      item.spec.storage_bytes === storage * GiB,
  );
  const save = useMutation({
    mutationFn: async (values: Fields) => {
      const item = items.find(
        (item) =>
          item.id + ':' + item.template_version === values.template &&
          item.spec.cpu_millicores === values.cpu &&
          item.spec.memory_bytes === values.memory &&
          item.spec.storage_bytes === values.storage * GiB,
      );
      if (!item || templates.isError) throw new Error('所选运行规格不可用，请重新加载');
      const nextLimits = baseQuota
        ? (Object.fromEntries(
            quotaDimensions.map((d) => {
              const raw = values[quotaFieldNames[d.key]] as number | undefined;
              return [d.key, raw !== undefined ? raw * d.scale : baseQuota.limits[d.key]];
            }),
          ) as unknown as OrganizationQuotaLimits)
        : null;
      const quotaChanged =
        !!baseQuota &&
        !!nextLimits &&
        quotaDimensions.some((d) => nextLimits[d.key] !== baseQuota.limits[d.key]);
      const defaultsChanged =
        !base ||
        base.template_id !== item.id ||
        base.template_version !== item.template_version ||
        base.spec_id !== item.spec.id ||
        base.max_user_instances !== values.max_user_instances ||
        base.retention_days !== values.retention_days;

      let savedDefaults: Defaults | undefined;
      let savedQuota: OrganizationQuota | undefined;

      if (quotaChanged && nextLimits && baseQuota) {
        const raiseQuotaFirst = values.max_user_instances > baseQuota.limits.max_instances;
        if (raiseQuotaFirst) {
          savedQuota = await client.saveOrganizationQuota(user, nextLimits, baseQuota.version);
          setBaseQuota(savedQuota);
        }
        if (defaultsChanged) {
          savedDefaults = await client.saveRuntimeDefaults(user, {
            version: base?.version ?? 0,
            template_id: item.id,
            template_version: item.template_version,
            spec_id: item.spec.id,
            max_user_instances: values.max_user_instances,
            retention_days: values.retention_days,
          });
          setBase(savedDefaults);
        }
        if (!raiseQuotaFirst) {
          const userInstancesChanged =
            defaultsChanged && values.max_user_instances !== baseQuota.default_user_instances;
          const expectedQuotaVersion = userInstancesChanged
            ? baseQuota.version + 1
            : baseQuota.version;
          savedQuota = await client.saveOrganizationQuota(user, nextLimits, expectedQuotaVersion);
          setBaseQuota(savedQuota);
        }
      } else {
        savedDefaults = await client.saveRuntimeDefaults(user, {
          version: base?.version ?? 0,
          template_id: item.id,
          template_version: item.template_version,
          spec_id: item.spec.id,
          max_user_instances: values.max_user_instances,
          retention_days: values.retention_days,
        });
        setBase(savedDefaults);
      }
      return { defaults: savedDefaults, quota: savedQuota };
    },
    onSuccess: (result) => {
      setOpen(false);
      onSaved(result);
    },
  });
  const reload = useMutation({
    mutationFn: async () => {
      const [nextDefaults, nextQuota] = await Promise.all([
        client.defaults(user),
        baseQuota ? client.organizationQuota(user).catch(() => baseQuota) : Promise.resolve(null),
      ]);
      return { defaults: nextDefaults, quota: nextQuota };
    },
    onSuccess: ({ defaults: nextDefaults, quota: nextQuota }) => {
      setBase(nextDefaults);
      setBaseQuota(nextQuota);
      form.resetFields();
      save.reset();
    },
  });
  useExpiredSession(reload.error);
  useExpiredSession(templates.error);
  useExpiredSession(save.error);
  useEffect(() => {
    if (!open || !items.length || form.getFieldValue('template')) return;
    const item =
      items.find(
        (item) =>
          base &&
          item.id === base.template_id &&
          item.template_version === base.template_version &&
          item.spec.id === base.spec_id,
      ) ??
      items.find((item) => item.spec.storage_bytes === 10 * GiB) ??
      items[0];
    const quotaInitial = baseQuota
      ? Object.fromEntries(
          quotaDimensions.map((d) => [quotaFieldNames[d.key], baseQuota.limits[d.key] / d.scale]),
        )
      : {};
    form.setFieldsValue({
      template: item.id + ':' + item.template_version,
      cpu: item.spec.cpu_millicores,
      memory: item.spec.memory_bytes,
      storage: item.spec.storage_bytes / GiB,
      max_user_instances: base?.max_user_instances ?? 2,
      retention_days: base?.retention_days ?? 7,
      ...quotaInitial,
    });
  }, [open, templates.data, base, baseQuota, form]);
  const show = () => {
    save.reset();
    reload.reset();
    setBase(configuration);
    setBaseQuota(quota);
    form.resetFields();
    setOpen(true);
  };
  const cpuOptions = Array.from(
    new Set(
      candidates
        .filter((item) => !memory || item.spec.memory_bytes === memory)
        .map((item) => item.spec.cpu_millicores),
    ),
  )
    .sort((a, b) => a - b)
    .map((value) => ({ value, label: `${value / 1000} 核` }));
  const memoryOptions = Array.from(
    new Set(
      candidates
        .filter((item) => !cpu || item.spec.cpu_millicores === cpu)
        .map((item) => item.spec.memory_bytes),
    ),
  )
    .sort((a, b) => a - b)
    .map((value) => ({ value, label: `${value / GiB} GiB` }));
  const storageOptions = Array.from(
    new Set(
      candidates
        .filter((item) => item.spec.cpu_millicores === cpu && item.spec.memory_bytes === memory)
        .map((item) => item.spec.storage_bytes / GiB),
    ),
  )
    .sort((a, b) => a - b)
    .map((value) => ({ value, label: `${value} GiB` }));
  if (!canEdit) return null;
  return (
    <>
      <Button type="primary" onClick={show}>
        {configuration ? '修改配置' : '初始化配置'}
      </Button>
      <Drawer
        title={configuration ? '修改默认资源配置' : '初始化默认资源配置'}
        width={560}
        open={open}
        onClose={() => {
          if (!save.isPending) setOpen(false);
        }}
        closable={!save.isPending}
        maskClosable={!save.isPending}
        keyboard={!save.isPending}
        destroyOnHidden
        footer={
          <div className="create-instance-footer">
            <Button disabled={save.isPending} onClick={() => setOpen(false)}>
              取消
            </Button>
            <Button
              type="primary"
              htmlType="submit"
              form={formID}
              loading={save.isPending}
              disabled={!selected || templates.isError}
            >
              保存配置
            </Button>
          </div>
        }
      >
        {templates.isPending ? (
          <Spin aria-label="加载批准运行规格" />
        ) : templates.isError ? (
          <Alert
            type="error"
            showIcon
            message="运行规格加载失败"
            action={<Button onClick={() => void templates.refetch()}>重新加载</Button>}
          />
        ) : !items.length ? (
          <Alert
            type="warning"
            showIcon
            message="尚无批准的运行模板和规格"
            description="请先完成运行环境初始化，再重新加载。"
            action={<Button onClick={() => void templates.refetch()}>重新加载</Button>}
          />
        ) : (
          <Form
            form={form}
            id={formID}
            layout="vertical"
            validateTrigger="onBlur"
            disabled={save.isPending}
            onFinish={(values) => save.mutate(values)}
          >
            <Form.Item name="template" hidden rules={[{ required: true }]}>
              <Input />
            </Form.Item>
            <Form.Item name="cpu" label="CPU" rules={[{ required: true, message: '请选择 CPU' }]}>
              <Select aria-label="默认 CPU" options={cpuOptions} />
            </Form.Item>
            <Form.Item
              name="memory"
              label="内存"
              rules={[{ required: true, message: '请选择内存' }]}
            >
              <Select aria-label="默认内存" options={memoryOptions} />
            </Form.Item>
            <Form.Item
              name="storage"
              label="存储（GiB）"
              dependencies={['cpu', 'memory', 'template']}
              rules={[
                { required: true, message: '请输入存储容量' },
                { type: 'integer', min: 1, max: 100, message: '请输入 1–100 的整数' },
                {
                  validator: (_, value) =>
                    !Number.isInteger(value) ||
                    value < 1 ||
                    value > 100 ||
                    storageOptions.some((option) => option.value === value)
                      ? Promise.resolve()
                      : Promise.reject(new Error('此存储容量尚未批准，请选择已批准容量')),
                },
              ]}
            >
              <InputNumber aria-label="默认存储" min={1} max={100} style={{ width: '100%' }} />
            </Form.Item>
            <Form.Item
              name="max_user_instances"
              label="每用户默认实例上限（个）"
              rules={[
                { required: true, message: '请输入实例上限' },
                { type: 'integer', min: 1, message: '请输入大于0的整数' },
              ]}
            >
              <InputNumber
                aria-label="每用户默认实例上限"
                min={1}
                precision={0}
                style={{ width: '100%' }}
              />
            </Form.Item>
            <Form.Item
              name="retention_days"
              label="数据保留期限（天）"
              rules={[
                { required: true, message: '请输入保留期限' },
                { type: 'integer', min: 1, max: 365, message: '请输入1–365的整数' },
              ]}
            >
              <InputNumber
                aria-label="数据保留期限"
                min={1}
                max={365}
                precision={0}
                style={{ width: '100%' }}
              />
            </Form.Item>
            {baseQuota && (
              <div className="runtime-defaults-drawer-quota">
                <div className="runtime-defaults-drawer-heading">
                  <h4>系统资源上限</h4>
                  <span>不能低于当前占用、默认单用户实例上限或已分配的用户额度</span>
                </div>
                <div className="organization-quota-fields">
                  {quotaDimensions.map((dimension) => {
                    const used = baseQuota.usage[dimension.key] / dimension.scale;
                    const fieldName = quotaFieldNames[dimension.key];
                    return (
                      <Form.Item
                        key={dimension.key}
                        name={fieldName}
                        dependencies={
                          dimension.key === 'max_instances' ? ['max_user_instances'] : undefined
                        }
                        label={`${dimension.label}上限（${dimension.unit}）`}
                        extra={`当前占用：${formatQuotaNumber(used)} ${dimension.unit}`}
                        rules={[
                          { required: true, message: '请填写系统资源上限' },
                          {
                            validator: (_, value: number | null | undefined) => {
                              if (
                                value == null ||
                                !Number.isSafeInteger(value * dimension.scale) ||
                                value < 1
                              )
                                return Promise.reject(new Error('请输入大于 0 的有效数值'));
                              if (value < used)
                                return Promise.reject(
                                  new Error(
                                    `不能低于当前占用 ${formatQuotaNumber(used)} ${dimension.unit}`,
                                  ),
                                );
                              if (
                                dimension.key === 'max_instances' &&
                                typeof form.getFieldValue('max_user_instances') === 'number' &&
                                value < form.getFieldValue('max_user_instances')
                              )
                                return Promise.reject(
                                  new Error('实例总上限不能低于每用户默认实例上限'),
                                );
                              return Promise.resolve();
                            },
                          },
                        ]}
                      >
                        <InputNumber
                          aria-label={`系统${dimension.label}上限`}
                          min={dimension.step}
                          step={dimension.step}
                          style={{ width: '100%' }}
                        />
                      </Form.Item>
                    );
                  })}
                </div>
              </div>
            )}
          </Form>
        )}
        {(save.isError || reload.isError) && (
          <Alert
            type="error"
            showIcon
            message={(reload.error ?? save.error)?.message}
            action={
              <Button loading={reload.isPending} onClick={() => reload.mutate()}>
                重新读取配置
              </Button>
            }
            style={{ marginTop: 16 }}
          />
        )}
      </Drawer>
    </>
  );
}

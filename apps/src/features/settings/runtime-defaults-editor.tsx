import { useEffect, useId, useState } from 'react';
import { useMutation, useQuery } from '@tanstack/react-query';
import { Alert, Button, Drawer, Form, Input, InputNumber, Select, Spin } from 'antd';
import { hasPermission, type CurrentUser } from '../../api/identity';
import { SettingsClient, type Defaults } from '../../api/settings';
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
}

export function RuntimeDefaultsEditor({ user, configuration, onSaved }: {
  user: CurrentUser; configuration: Defaults | null | undefined; onSaved: (value: Defaults) => void;
}) {
  const [open, setOpen] = useState(false);
  const [base, setBase] = useState(configuration);
  const [form] = Form.useForm<Fields>();
  const formID = useId();
  const canEdit = hasPermission(user, 'defaults:write');
  const templates = useQuery({
    queryKey: ['settings', user.id, 'templates'], enabled: open && canEdit,
    queryFn: ({ signal }) => client.templates(user, signal), retry: false,
  });
  const items = templates.data?.items ?? [];
  const template = Form.useWatch('template', form);
  const cpu = Form.useWatch('cpu', form);
  const memory = Form.useWatch('memory', form);
  const storage = Form.useWatch('storage', form);
  const candidates = items.filter(item => item.id + ':' + item.template_version === template);
  const selected = candidates.find(item => item.spec.cpu_millicores === cpu && item.spec.memory_bytes === memory && item.spec.storage_bytes === storage * GiB);
  const save = useMutation({
    mutationFn: async (values: Fields) => {
      const item = items.find(item => item.id + ':' + item.template_version === values.template && item.spec.cpu_millicores === values.cpu && item.spec.memory_bytes === values.memory && item.spec.storage_bytes === values.storage * GiB);
      if (!item || templates.isError) throw new Error('所选运行规格不可用，请重新加载');
      return client.saveRuntimeDefaults(user, { version: base?.version ?? 0, template_id: item.id, template_version: item.template_version, spec_id: item.spec.id, max_user_instances: values.max_user_instances, retention_days: values.retention_days });
    },
    onSuccess: value => { setOpen(false); onSaved(value); },
  });
  const reload = useMutation({ mutationFn: () => client.defaults(user), onSuccess: value => { setBase(value); form.resetFields(); save.reset(); } });
  useExpiredSession(reload.error);
  useExpiredSession(templates.error);
  useExpiredSession(save.error);
  useEffect(() => {
    if (!open || !items.length || form.getFieldValue('template')) return;
    const item = items.find(item => base && item.id === base.template_id && item.template_version === base.template_version && item.spec.id === base.spec_id) ?? items.find(item => item.spec.storage_bytes === 10 * GiB) ?? items[0];
    form.setFieldsValue({ template: item.id + ':' + item.template_version, cpu: item.spec.cpu_millicores, memory: item.spec.memory_bytes, storage: item.spec.storage_bytes / GiB, max_user_instances: base?.max_user_instances ?? 2, retention_days: base?.retention_days ?? 7 });
  }, [open, templates.data, base, form]);
  const show = () => { save.reset(); reload.reset(); setBase(configuration); form.resetFields(); setOpen(true); };
  const cpuOptions = Array.from(new Set(candidates.filter(item => !memory || item.spec.memory_bytes === memory).map(item => item.spec.cpu_millicores))).sort((a, b) => a - b).map(value => ({ value, label: `${value / 1000} 核` }));
  const memoryOptions = Array.from(new Set(candidates.filter(item => !cpu || item.spec.cpu_millicores === cpu).map(item => item.spec.memory_bytes))).sort((a, b) => a - b).map(value => ({ value, label: `${value / GiB} GiB` }));
  const storageOptions = Array.from(new Set(candidates.filter(item => item.spec.cpu_millicores === cpu && item.spec.memory_bytes === memory).map(item => item.spec.storage_bytes / GiB))).sort((a, b) => a - b).map(value => ({ value, label: `${value} GiB` }));
  useEffect(() => {
    if (!open || !storageOptions.length || storageOptions.some(option => option.value === storage)) return;
    form.setFieldValue('storage', storageOptions[0].value);
  }, [open, storage, storageOptions, form]);
  if (!canEdit) return null;
  return <>
    <Button type="primary" onClick={show}>{configuration ? '修改配置' : '初始化配置'}</Button>
    <Drawer title={configuration ? '修改默认用户资源配置' : '初始化默认用户资源配置'} width={560} open={open} onClose={() => { if (!save.isPending) setOpen(false); }} closable={!save.isPending} maskClosable={!save.isPending} keyboard={!save.isPending} destroyOnHidden footer={<div className="create-instance-footer"><Button disabled={save.isPending} onClick={() => setOpen(false)}>取消</Button><Button type="primary" htmlType="submit" form={formID} loading={save.isPending} disabled={!selected || templates.isError}>保存配置</Button></div>}>
      {templates.isPending ? <Spin aria-label="加载批准运行规格" /> : templates.isError ? <Alert type="error" showIcon message="运行规格加载失败" action={<Button onClick={() => void templates.refetch()}>重新加载</Button>} /> : !items.length ? <Alert type="warning" showIcon message="尚无批准的运行模板和规格" description="请先完成运行环境初始化，再重新加载。" action={<Button onClick={() => void templates.refetch()}>重新加载</Button>} /> : <Form form={form} id={formID} layout="vertical" validateTrigger="onBlur" disabled={save.isPending} onFinish={values => save.mutate(values)}>
        <Form.Item name="template" hidden rules={[{ required: true }]}><Input /></Form.Item>
        <Form.Item name="cpu" label="CPU" rules={[{ required: true, message: '请选择 CPU' }]}><Select aria-label="默认 CPU" options={cpuOptions} /></Form.Item>
        <Form.Item name="memory" label="内存" rules={[{ required: true, message: '请选择内存' }]}><Select aria-label="默认内存" options={memoryOptions} /></Form.Item>
        <Form.Item name="storage" label="存储" dependencies={['cpu', 'memory', 'template']} rules={[{ required: true, message: '请选择存储容量' }, { type: 'integer', min: 1, max: 100, message: '请选择 1–100 GiB 的容量' }, { validator: (_, value) => !Number.isInteger(value) || value < 1 || value > 100 || storageOptions.some(option => option.value === value) ? Promise.resolve() : Promise.reject(new Error('此组合下没有该存储规格，请重新选择')) }]}><Select aria-label="默认存储" placeholder="请选择存储容量" options={storageOptions} /></Form.Item>
        <Form.Item name="max_user_instances" label="每用户默认实例上限（个）" rules={[{ required: true, message: '请输入实例上限' }, { type: 'integer', min: 1, message: '请输入大于0的整数' }]}><InputNumber aria-label="每用户默认实例上限" min={1} precision={0} style={{ width: '100%' }} /></Form.Item>
        <Form.Item name="retention_days" label="数据保留期限（天）" rules={[{ required: true, message: '请输入保留期限' }, { type: 'integer', min: 1, max: 365, message: '请输入1–365的整数' }]}><InputNumber aria-label="数据保留期限" min={1} max={365} precision={0} style={{ width: '100%' }} /></Form.Item>
      </Form>}
      {(save.isError || reload.isError) && <Alert type="error" showIcon message={(reload.error ?? save.error)?.message} action={<Button loading={reload.isPending} onClick={() => reload.mutate()}>重新读取配置</Button>} style={{ marginTop: 16 }} />}
    </Drawer>
  </>;
}

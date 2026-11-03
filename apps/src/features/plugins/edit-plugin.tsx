import { useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Form, Input, Modal, Select } from 'antd';
import { useState } from 'react';
import { APIError } from '../../api/error';
import { type CurrentUser } from '../../api/identity';
import { type PluginMetadata } from '../../api/plugins';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';
import { errorMessage } from '../../shared/utils/error-message';
import { pluginCategories, type CatalogPlugin } from './plugin-catalog';

import { client } from './plugin-client';

export type PluginFormValues = Omit<PluginMetadata, 'keywords'> & { keywords: string };

export const metadataValues = (plugin: CatalogPlugin): PluginFormValues => ({
  name: plugin.name,
  summary: plugin.summary,
  description: plugin.description,
  author: plugin.author,
  version: plugin.version,
  category: plugin.category,
  keywords: plugin.keywords.join(', '),
  repository: plugin.repository,
  documentation: plugin.documentation,
  compatibility: plugin.compatibility,
  checkedAt: plugin.checkedAt.slice(0, 10),
  sourceNote: plugin.sourceNote,
});

export function EditPlugin({
  user,
  plugin,
  onClose,
  onSaved,
}: {
  user: CurrentUser;
  plugin: CatalogPlugin;
  onClose: () => void;
  onSaved: (plugin: CatalogPlugin) => void;
}) {
  const cache = useQueryClient();
  const [form] = Form.useForm<PluginFormValues>();
  const [current, setCurrent] = useState(plugin);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState<unknown>(null);
  const [conflict, setConflict] = useState(false);
  useExpiredSession(error);
  async function save(values: PluginFormValues) {
    if (busy || conflict) return;
    setBusy('save');
    setError(null);
    try {
      onSaved(
        await client.update(user, current, {
          ...values,
          keywords: values.keywords
            .split(/[,，\n]/)
            .map((value) => value.trim())
            .filter(Boolean),
        }),
      );
    } catch (cause) {
      setError(cause);
      setConflict(cause instanceof APIError && cause.code === 'PLUGIN_VERSION_CONFLICT');
    } finally {
      setBusy('');
    }
  }
  async function reload() {
    if (busy) return;
    setBusy('reload');
    try {
      const result = await client.list(user);
      const fresh = result.items.find((item) => item.packageName === current.packageName);
      if (!fresh) throw new Error('插件已不存在，请关闭后重新查看列表');
      cache.setQueryData(['plugins', user.id], result);
      setCurrent(fresh);
      form.setFieldsValue(metadataValues(fresh));
      setConflict(false);
      setError(null);
    } catch (cause) {
      setError(cause);
    } finally {
      setBusy('');
    }
  }
  return (
    <Modal
      className="plugin-edit-modal"
      open
      title="编辑插件信息"
      width={680}
      okText="保存"
      cancelText="取消"
      confirmLoading={busy === 'save'}
      okButtonProps={{
        disabled: !!busy || conflict,
        'aria-label': '保存',
        'aria-busy': busy === 'save',
      }}
      cancelButtonProps={{ disabled: !!busy }}
      closable={!busy}
      maskClosable={!busy}
      keyboard={!busy}
      onCancel={() => {
        if (!busy) onClose();
      }}
      onOk={() => {
        if (!busy) form.submit();
      }}
    >
      <p className="plugin-edit-package">{plugin.packageName}</p>
      <Form
        form={form}
        layout="vertical"
        initialValues={metadataValues(plugin)}
        disabled={!!busy}
        onFinish={(values) => void save(values)}
      >
        <Form.Item
          name="name"
          label="插件名称"
          rules={[{ required: true, whitespace: true, message: '请填写插件名称' }]}
        >
          <Input aria-label="插件名称" maxLength={214} />
        </Form.Item>
        <Form.Item name="summary" label="简介">
          <Input.TextArea aria-label="简介" rows={2} maxLength={1000} />
        </Form.Item>
        <Form.Item name="description" label="详细介绍">
          <Input.TextArea aria-label="详细介绍" rows={3} maxLength={4000} />
        </Form.Item>
        <div className="plugin-edit-grid">
          <Form.Item
            name="author"
            label="发布方"
            rules={[{ required: true, whitespace: true, message: '请填写发布方' }]}
          >
            <Input aria-label="发布方" maxLength={200} />
          </Form.Item>
          <Form.Item
            name="version"
            label="收录版本"
            rules={[{ required: true, whitespace: true, message: '请填写收录版本' }]}
          >
            <Input aria-label="收录版本" maxLength={128} />
          </Form.Item>
          <Form.Item
            name="category"
            label="分类"
            rules={[{ required: true, message: '请选择分类' }]}
          >
            <Select
              aria-label="分类"
              options={pluginCategories.map((value) => ({ value, label: value }))}
            />
          </Form.Item>
          <Form.Item
            name="checkedAt"
            label="核对日期"
            rules={[{ required: true, message: '请选择核对日期' }]}
          >
            <Input aria-label="核对日期" type="date" />
          </Form.Item>
        </div>
        <Form.Item name="compatibility" label="版本要求">
          <Input.TextArea aria-label="版本要求" rows={2} maxLength={2000} />
        </Form.Item>
        <Form.Item name="sourceNote" label="来源说明">
          <Input.TextArea aria-label="来源说明" rows={2} maxLength={4000} />
        </Form.Item>
        <Form.Item name="keywords" label="关键词" extra="多个关键词用逗号分隔。">
          <Input aria-label="关键词" maxLength={1220} />
        </Form.Item>
        <Form.Item name="documentation" label="文档链接">
          <Input aria-label="文档链接" type="url" maxLength={2048} placeholder="https://" />
        </Form.Item>
        <Form.Item name="repository" label="源码链接">
          <Input aria-label="源码链接" type="url" maxLength={2048} placeholder="https://" />
        </Form.Item>
      </Form>
      {!!error && (
        <Alert
          type="error"
          showIcon
          message={errorMessage(error)}
          description={conflict ? '当前输入已保留。重新载入会读取已保存的信息。' : undefined}
          action={
            conflict ? (
              <Button loading={busy === 'reload'} disabled={!!busy} onClick={() => void reload()}>
                重新载入
              </Button>
            ) : undefined
          }
        />
      )}
    </Modal>
  );
}

import { Alert, Button, Descriptions, Form, Input, Modal, Segmented, Select } from 'antd';
import { Upload } from 'lucide-react';
import { useRef, useState } from 'react';
import { type CurrentUser } from '../../api/identity';
import { type PluginArchive } from '../../api/plugins';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';
import { errorMessage } from '../../shared/utils/error-message';
import { pluginCategories, type CatalogPlugin, type PluginCategory } from './plugin-catalog';

import { client } from './plugin-client';

export function AddPlugin({
  user,
  onClose,
  onAdded,
}: {
  user: CurrentUser;
  onClose: () => void;
  onAdded: (plugin: CatalogPlugin) => void;
}) {
  const fileInput = useRef<HTMLInputElement>(null);
  const [method, setMethod] = useState<'online' | 'offline'>('online');
  const [file, setFile] = useState<File | null>(null);
  const [archive, setArchive] = useState<PluginArchive | null>(null);
  const [packageName, setPackageName] = useState('');
  const [category, setCategory] = useState<PluginCategory>('集成扩展');
  const [preview, setPreview] = useState<CatalogPlugin | null>(null);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [failure, setFailure] = useState<unknown>(null);
  useExpiredSession(failure);
  async function run(action: string, task: () => Promise<void>) {
    if (busy) return;
    setBusy(action);
    setError('');
    setFailure(null);
    try {
      await task();
    } catch (cause) {
      setFailure(cause);
      setError(errorMessage(cause));
    } finally {
      setBusy('');
    }
  }
  const read = () => {
    if (method === 'online' ? !packageName.trim() : !file) return;
    void run('read', async () => {
      setPreview(null);
      if (method === 'online') {
        setPreview(await client.preview(user, packageName.trim()));
        return;
      }
      const content = await new Promise<string>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(String(reader.result).split(',')[1]);
        reader.onerror = () => reject(new Error('无法读取插件包，请重新选择文件'));
        reader.readAsDataURL(file!);
      });
      const uploaded = { filename: file!.name, content_base64: content };
      setArchive(uploaded);
      setPreview(await client.previewArchive(user, uploaded));
    });
  };
  const chooseFile = (selected?: File) => {
    setFile(null);
    setArchive(null);
    setPreview(null);
    setError('');
    if (!selected) return;
    if (!/\.(tgz|tar\.gz)$/i.test(selected.name)) {
      setError('请选择 .tgz 或 .tar.gz 插件包');
      return;
    }
    if (selected.size > 5 * 1024 * 1024) {
      setError('插件包不能超过 5 MiB');
      return;
    }
    setFile(selected);
  };
  return (
    <Modal
      className="plugin-add-modal"
      open
      title="添加插件"
      width={560}
      okText="添加"
      cancelText="取消"
      confirmLoading={busy === 'save'}
      okButtonProps={{
        disabled: !preview || !!busy,
        'aria-label': '添加',
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
        if (preview)
          void run('save', async () =>
            onAdded(
              method === 'offline' && archive
                ? await client.addArchive(user, archive, category)
                : await client.add(user, preview, category),
            ),
          );
      }}
    >
      <Form layout="vertical">
        <Form.Item label="添加方式">
          <Segmented
            aria-label="添加方式"
            block
            value={method}
            options={[
              { value: 'online', label: '在线' },
              { value: 'offline', label: '离线' },
            ]}
            disabled={!!busy}
            onChange={(value) => {
              setMethod(value as 'online' | 'offline');
              setPreview(null);
              setArchive(null);
              setError('');
            }}
          />
        </Form.Item>
        {method === 'online' ? (
          <Form.Item
            label="npm 包名"
            htmlFor="plugin-package-name"
            required
            extra="可填写包名或指定版本，例如 @scope/plugin 或 @scope/plugin@1.2.3。"
          >
            <Input
              id="plugin-package-name"
              aria-label="npm 包名"
              aria-required="true"
              autoFocus
              maxLength={300}
              placeholder="@scope/plugin"
              value={packageName}
              disabled={!!busy}
              onPressEnter={read}
              onChange={(event) => {
                setPackageName(event.target.value);
                setPreview(null);
                setError('');
              }}
            />
          </Form.Item>
        ) : (
          <Form.Item
            label="插件压缩包"
            htmlFor="plugin-archive"
            required
            extra="支持 .tgz、.tar.gz，最大 5 MiB。"
          >
            <input
              ref={fileInput}
              id="plugin-archive"
              type="file"
              hidden
              style={{ display: 'none' }}
              aria-label="插件压缩包"
              aria-required="true"
              accept=".tgz,.tar.gz"
              disabled={!!busy}
              onChange={(event) => {
                chooseFile(event.target.files?.[0]);
                event.target.value = '';
              }}
            />
            <Button
              icon={<Upload size={16} aria-hidden="true" />}
              disabled={!!busy}
              onClick={() => fileInput.current?.click()}
            >
              选择插件包
            </Button>
            {file && <p className="plugin-upload-filename">{file.name}</p>}
          </Form.Item>
        )}
        <Form.Item label="分类" htmlFor="plugin-add-category">
          <Select
            id="plugin-add-category"
            aria-label="插件分类"
            value={category}
            disabled={!!busy}
            options={pluginCategories.map((value) => ({ value, label: value }))}
            onChange={setCategory}
          />
        </Form.Item>
        <Button
          aria-label="读取信息"
          aria-busy={busy === 'read'}
          loading={busy === 'read'}
          disabled={(method === 'online' ? !packageName.trim() : !file) || !!busy}
          onClick={read}
        >
          读取信息
        </Button>
      </Form>
      {busy === 'read' && (
        <p role="status" className="context-note">
          {method === 'online' ? '正在读取 npm 包信息…' : '正在读取插件包信息…'}
        </p>
      )}
      {preview && (
        <section className="plugin-add-preview" aria-label="插件信息">
          <Descriptions
            size="small"
            column={1}
            items={[
              { key: 'name', label: '名称', children: preview.name },
              { key: 'package', label: '包名', children: preview.packageName },
              { key: 'version', label: '版本', children: preview.version },
              { key: 'author', label: '发布方', children: preview.author },
            ]}
          />
          <p>{preview.summary}</p>
        </section>
      )}
      {error && <Alert type="error" showIcon message={error} />}
    </Modal>
  );
}

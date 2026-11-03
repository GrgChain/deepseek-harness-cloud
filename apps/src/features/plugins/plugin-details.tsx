import { Alert, Button, Descriptions, Drawer, Tag } from 'antd';
import { Check, Copy, Download, ExternalLink, Pencil } from 'lucide-react';
import { useRef, useState } from 'react';
import { hasPermission, type CurrentUser } from '../../api/identity';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';
import { errorMessage } from '../../shared/utils/error-message';
import { isBundledMem0Plugin, mem0PluginPackage, type CatalogPlugin } from './plugin-catalog';

import { client } from './plugin-client';

import { iconForPlugin } from './plugin-icons';

import { EditPlugin } from './edit-plugin';

export function PluginDetails({
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
  const Icon = iconForPlugin(plugin);
  const editButton = useRef<HTMLButtonElement>(null);
  const [editing, setEditing] = useState(false);
  const closeEditor = () => {
    setEditing(false);
    requestAnimationFrame(() => editButton.current?.focus());
  };
  const offline = plugin.additionMethod === 'offline';
  const bundledMem0 = isBundledMem0Plugin(plugin);
  const mem0Package = plugin.packageName === mem0PluginPackage.name;
  const [downloading, setDownloading] = useState(false);
  const [downloadError, setDownloadError] = useState<unknown>(null);
  useExpiredSession(downloadError);
  const [copiedSpec, setCopiedSpec] = useState('');
  const [copying, setCopying] = useState(false);
  const [copyError, setCopyError] = useState(false);
  const spec = plugin.packageName + '@' + plugin.version;
  const copied = copiedSpec === spec;
  const download = async () => {
    if (downloading) return;
    setDownloading(true);
    setDownloadError(null);
    try {
      let blob: Blob;
      if (bundledMem0) {
        const response = await fetch(mem0PluginPackage.path, {
          credentials: 'same-origin',
          signal: AbortSignal.timeout(22_000),
        });
        if (!response.ok) throw new Error('插件包暂不可用，请稍后重试');
        blob = await response.blob();
        const header = new Uint8Array(await blob.slice(0, 2).arrayBuffer());
        if (header[0] !== 0x1f || header[1] !== 0x8b) throw new Error('插件包暂不可用，请稍后重试');
      } else blob = await client.download(plugin.id);
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = bundledMem0 ? mem0PluginPackage.filename : plugin.filename || 'plugin.tgz';
      document.body.append(link);
      link.click();
      link.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch (cause) {
      setDownloadError(cause);
    } finally {
      setDownloading(false);
    }
  };
  const copy = async () => {
    setCopying(true);
    setCopyError(false);
    try {
      await navigator.clipboard.writeText(spec);
      setCopiedSpec(spec);
    } catch {
      setCopiedSpec('');
      setCopyError(true);
    } finally {
      setCopying(false);
    }
  };
  return (
    <Drawer
      className="plugin-detail-drawer"
      title="插件详情"
      width={620}
      open
      onClose={onClose}
      keyboard={!editing}
      maskClosable={!editing}
      closable={!editing}
      extra={
        hasPermission(user, 'plugins:manage') ? (
          <Button
            ref={editButton}
            icon={<Pencil size={15} aria-hidden="true" />}
            disabled={editing || downloading}
            onClick={() => setEditing(true)}
          >
            编辑信息
          </Button>
        ) : undefined
      }
    >
      <div className="plugin-detail-heading">
        <span className="plugin-card-symbol" aria-hidden="true">
          <Icon size={25} />
        </span>
        <div>
          <h2>{plugin.name}</h2>
          <Tag>{bundledMem0 ? '本地维护' : '第三方'}</Tag>
          {offline && <Tag>离线</Tag>}
          <span className="plugin-detail-category">{plugin.category}</span>
        </div>
      </div>
      <p className="plugin-detail-description">{plugin.description}</p>
      <Descriptions
        size="small"
        column={1}
        items={[
          { key: 'author', label: '发布方', children: plugin.author },
          { key: 'version', label: '收录版本', children: plugin.version },
          { key: 'compatibility', label: '版本要求', children: plugin.compatibility },
          {
            key: 'checked',
            label: '来源核对',
            children:
              plugin.checkedAt.slice(0, 10) + (plugin.sourceNote ? ' · ' + plugin.sourceNote : ''),
          },
          ...(offline
            ? [
                { key: 'filename', label: '原包文件', children: plugin.filename },
                { key: 'sha256', label: 'SHA-256', children: <code>{plugin.sha256}</code> },
              ]
            : []),
        ]}
      />
      <div className="plugin-detail-links">
        {plugin.documentation && (
          <a href={plugin.documentation} target="_blank" rel="noopener noreferrer">
            使用文档
            <ExternalLink size={14} aria-hidden="true" />
          </a>
        )}
        {plugin.repository && (
          <a href={plugin.repository} target="_blank" rel="noopener noreferrer">
            源代码
            <ExternalLink size={14} aria-hidden="true" />
          </a>
        )}
        {!offline && !mem0Package && (
          <a
            href={'https://www.npmjs.com/package/' + plugin.packageName + '/v/' + plugin.version}
            target="_blank"
            rel="noopener noreferrer"
          >
            npm 包<ExternalLink size={14} aria-hidden="true" />
          </a>
        )}
      </div>
      <section className="plugin-install-guide" aria-label="使用方式">
        <h3>在 DSH 中使用</h3>
        {bundledMem0 ? (
          <>
            <p>此插件由 Harness Cloud 维护，非 Mem0 官方插件，通过本地插件包分发。</p>
            <ol>
              <li>下载插件包，并将文件传到目标工作空间可读取的位置。</li>
              <li>
                在 DSH“插件”页选择“添加插件”，输入工作空间内插件包的绝对路径，按原生提示安装。
              </li>
              <li>填写下方连接配置，再启用插件；详细步骤见包内 README。</li>
            </ol>
          </>
        ) : offline ? (
          <p>下载原包，按插件文档在 DSH 中安装本地插件包。</p>
        ) : mem0Package ? (
          <p>此版本没有随平台分发的插件包，请核对版本与来源说明。</p>
        ) : (
          <ol>
            <li>复制下方包名和版本。</li>
            <li>在 DSH“插件”页选择“添加插件”，粘贴包名。</li>
            <li>查看版本和依赖检查结果，按原生提示确认后安装。</li>
          </ol>
        )}
        {(offline || bundledMem0) && (
          <>
            <Button
              icon={<Download size={16} aria-hidden="true" />}
              aria-label="下载插件包"
              loading={downloading}
              disabled={downloading}
              onClick={() => void download()}
            >
              下载插件包
            </Button>
            {!!downloadError && (
              <Alert type="error" showIcon message={errorMessage(downloadError)} />
            )}
          </>
        )}
        {bundledMem0 && (
          <section aria-label="Mem0 配置说明">
            <h3>连接与自动记忆</h3>
            <Descriptions
              size="small"
              column={1}
              items={[
                {
                  key: 'endpoint',
                  label: 'endpoint',
                  children: '工作空间可访问的 Mem0 OSS REST 服务地址，不使用 Mem0 云平台 API。',
                },
                {
                  key: 'apiKey',
                  label: 'apiKey',
                  children: '该服务的 API Key，由你在 DSH 插件配置中填写。',
                },
                {
                  key: 'userId',
                  label: 'userId',
                  children: '记忆所属用户的固定标识；同一服务下保持一致可跨会话召回。',
                },
                {
                  key: 'autoCapture',
                  label: 'autoCapture',
                  children: '默认关闭，需显式开启；默认只保存用户消息，infer 默认为 false。',
                },
              ]}
            />
            <p>
              插件根据当前输入自动召回相关记忆。开启自动记录后，用户消息会发送到所填服务；助手回复需另外开启。连接凭据保存在工作空间插件配置中，与平台记忆连接分别配置；插件保存的内容不会自动出现在平台记忆连接的内容列表。
            </p>
          </section>
        )}
        <div className="plugin-install-spec">
          <code tabIndex={0} aria-label="插件安装包名">
            {spec}
          </code>
          <Button
            icon={
              copied ? (
                <Check size={15} aria-hidden="true" />
              ) : (
                <Copy size={15} aria-hidden="true" />
              )
            }
            loading={copying}
            onClick={() => void copy()}
          >
            {copied ? '已复制' : '复制包名'}
          </Button>
        </div>
        <span className="plugin-copy-status" role="status" aria-live="polite">
          {copied ? '包名与版本已复制' : ''}
        </span>
        {copyError && (
          <Alert type="warning" showIcon message="复制失败，请手动选择并复制上方包名" />
        )}
        <p className="plugin-install-note">安装与启用由你在 DSH 中完成，插件仅提供目录和说明。</p>
      </section>
      {editing && (
        <EditPlugin
          user={user}
          plugin={plugin}
          onClose={closeEditor}
          onSaved={(updated) => {
            closeEditor();
            onSaved(updated);
          }}
        />
      )}
    </Drawer>
  );
}

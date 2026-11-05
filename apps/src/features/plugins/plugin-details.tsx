import { Alert, Button, Descriptions, Drawer, Tag } from 'antd';
import { Check, ChevronDown, Copy, Download, ExternalLink, Pencil } from 'lucide-react';
import { useRef, useState } from 'react';
import { hasPermission, type CurrentUser } from '../../api/identity';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';
import { errorMessage } from '../../shared/utils/error-message';
import {
  bundledMemoryPlugin,
  isLocalMemoryPackage,
  graphitiPluginPackage,
  type CatalogPlugin,
} from './plugin-catalog';

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
  const bundled = bundledMemoryPlugin(plugin);
  const localPackage = isLocalMemoryPackage(plugin.packageName);
  const graphiti = plugin.packageName === graphitiPluginPackage.name;
  const downloadable = offline || !!bundled;
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
      if (bundled) {
        const response = await fetch(bundled.path, {
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
      link.download = bundled ? bundled.filename : plugin.filename || 'plugin.tgz';
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
      rootClassName="plugin-detail-root"
      title="插件详情"
      width={720}
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
        <div className="plugin-detail-identity">
          <h2>{plugin.name}</h2>
          <div className="plugin-detail-tags">
            <Tag>v{plugin.version}</Tag>
            <Tag>{bundled ? '本地维护' : '第三方'}</Tag>
            {offline && <Tag>离线</Tag>}
            <span className="plugin-detail-category">{plugin.category}</span>
          </div>
        </div>
      </div>
      <p className="plugin-detail-description">{plugin.description}</p>
      <dl className="plugin-detail-facts">
        <div>
          <dt>发布方</dt>
          <dd>{plugin.author}</dd>
        </div>
        <div>
          <dt>版本要求</dt>
          <dd>{plugin.compatibility}</dd>
        </div>
      </dl>
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
        {!offline && !localPackage && (
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
        <div className="plugin-detail-section-heading">
          <h3>安装插件</h3>
          <span>{downloadable ? '本地插件包' : localPackage ? '版本待核对' : 'npm 安装'}</span>
        </div>
        <div className="plugin-install-package">
          <span className="plugin-detail-label">包名与版本</span>
          <code tabIndex={0} aria-label="插件安装包名">
            {spec}
          </code>
          <div className="plugin-install-actions">
            {downloadable && (
              <Button
                type="primary"
                icon={<Download size={16} aria-hidden="true" />}
                aria-label="下载插件包"
                loading={downloading}
                disabled={downloading}
                onClick={() => void download()}
              >
                下载插件包
              </Button>
            )}
            <Button
              type={downloadable || localPackage ? 'default' : 'primary'}
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
            <span className="plugin-copy-status" role="status" aria-live="polite">
              {copied ? '包名与版本已复制' : ''}
            </span>
          </div>
          {!!downloadError && <Alert type="error" showIcon message={errorMessage(downloadError)} />}
          {copyError && (
            <Alert type="warning" showIcon message="复制失败，请手动选择并复制上方包名" />
          )}
        </div>
        {bundled && graphiti ? (
          <>
            <p>由 Harness Cloud 维护，非 Graphiti 官方插件。</p>
            <p>新建工作空间选择 Graphiti 记忆并开启完整会话同步后，DSH 会自动安装并启用。</p>
            <p>
              包含 graphiti-memory 技能，通过 MCP
              检索记忆和查看同步状态。此版本使用平台记忆连接，凭据与图谱分组由平台配置。
            </p>
          </>
        ) : bundled ? (
          <>
            <p>由 Harness Cloud 维护，非 Mem0 官方插件。</p>
            <p>
              新建工作空间选择 Mem0 记忆并开启完整会话同步后，DSH
              会自动安装并启用。独立安装可按以下步骤操作。
            </p>
            <ol className="plugin-install-steps">
              <li>
                <strong>下载并上传</strong>
                <p>将插件包上传到目标工作空间可读取的位置。</p>
              </li>
              <li>
                <strong>在 DSH 中安装</strong>
                <p>打开“插件 → 添加插件”，输入插件包的绝对路径，按提示安装。</p>
              </li>
              <li>
                <strong>配置后启用</strong>
                <p>按包内 README 完成配置，再启用插件。</p>
              </li>
            </ol>
          </>
        ) : offline ? (
          <ol className="plugin-install-steps">
            <li>
              <strong>下载并上传</strong>
              <p>将原包上传到目标工作空间可读取的位置。</p>
            </li>
            <li>
              <strong>在 DSH 中安装</strong>
              <p>打开“插件 → 添加插件”，输入插件包的绝对路径，按插件文档完成安装与配置。</p>
            </li>
          </ol>
        ) : localPackage ? (
          <p>此版本没有随平台分发的插件包，请核对版本与来源说明。</p>
        ) : (
          <ol className="plugin-install-steps">
            <li>
              <strong>复制包名与版本</strong>
              <p>使用上方包名，安装当前收录版本。</p>
            </li>
            <li>
              <strong>在 DSH 中安装</strong>
              <p>打开“插件 → 添加插件”，粘贴包名。</p>
            </li>
            <li>
              <strong>确认并启用</strong>
              <p>核对版本和依赖检查结果，按提示安装、配置并启用。</p>
            </li>
          </ol>
        )}
        <p className="plugin-install-note">
          {bundled && graphiti
            ? '旧工作空间需新建后使用原生插件。'
            : bundled
              ? '独立安装与启用在 DSH 中完成。'
              : '安装与启用由你在 DSH 中完成。'}
        </p>
      </section>
      <details className="plugin-detail-provenance">
        <summary>
          来源与校验
          <ChevronDown size={16} aria-hidden="true" />
        </summary>
        <Descriptions
          size="small"
          column={1}
          items={[
            { key: 'checked', label: '来源核对', children: plugin.checkedAt.slice(0, 10) },
            ...(plugin.sourceNote
              ? [{ key: 'source', label: '来源说明', children: plugin.sourceNote }]
              : []),
            ...(offline
              ? [
                  { key: 'filename', label: '原包文件', children: plugin.filename },
                  { key: 'sha256', label: 'SHA-256', children: <code>{plugin.sha256}</code> },
                ]
              : []),
          ]}
        />
      </details>
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

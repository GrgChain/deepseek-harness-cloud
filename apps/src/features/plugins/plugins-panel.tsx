import { useState } from 'react';
import { Alert, Button, Descriptions, Drawer, Empty, Input, Select, Tag } from 'antd';
import { BadgeCheck, Brain, Check, Copy, ExternalLink, GitBranch, Heading, Mic, Puzzle, Repeat2, ScanSearch, Search, SquareTerminal, Store, Terminal, Users, type LucideIcon } from 'lucide-react';
import { DataPagination, usePagePagination } from '../../shared/components/data-pagination';
import { catalogCheckedAt, filterPlugins, pluginCategories, type CatalogPlugin } from './plugin-catalog';
import './plugins.css';

export const pluginIcons: Record<string, LucideIcon> = {
 'session-search': Search,
 'openviking-memory': Brain,
 'terminal-bundle': Terminal,
 'dsh-ssh': SquareTerminal,
 'agent-team-profile': Users,
 'voice-input-bundle': Mic,
 dshmarket: Store,
 'session-titles-bundle': Heading,
 'tool-worktree': GitBranch,
 'ralph-bundle': Repeat2,
 'inspector-profile': ScanSearch,
 'badge-skill-bundle': BadgeCheck,
};
const sourceLabels = { official: '官方', community: '第三方' };

export function PluginsPanel() {
 const [search, setSearch] = useState('');
 const [category, setCategory] = useState('all');
 const [source, setSource] = useState('all');
 const [selected, setSelected] = useState<CatalogPlugin | null>(null);
 const pagination = usePagePagination(JSON.stringify([search, category, source]));
 const matches = filterPlugins(search, category, source);
 const items = matches.slice((pagination.page - 1) * pagination.pageSize, pagination.page * pagination.pageSize);
 return <section className="plugins-panel" aria-label="插件">
  <div className="provider-page-heading"><div><h2>发现插件</h2><p>浏览官方与第三方扩展，让工作空间拥有更多能力。</p></div></div>
  <div className="plugin-catalog-toolbar">
   <Input role="searchbox" aria-label="搜索插件" placeholder="搜索名称、功能、包名或作者" prefix={<Search size={16} aria-hidden="true" />} value={search} onChange={event => setSearch(event.target.value)} allowClear />
   <div className="plugin-catalog-filters">
    <label><span>分类</span><Select aria-label="插件分类" value={category} onChange={setCategory} options={[{ value: 'all', label: '全部分类' }, ...pluginCategories.map(value => ({ value, label: value }))]} /></label>
    <label><span>来源</span><Select aria-label="插件来源" value={source} onChange={setSource} options={[{ value: 'all', label: '全部来源' }, { value: 'official', label: '官方' }, { value: 'community', label: '第三方' }]} /></label>
   </div>
  </div>
  <div className="plugin-catalog-grid" aria-label="插件目录">
   {items.map(plugin => {
    const Icon = pluginIcons[plugin.id] ?? Puzzle;
    return <article className="plugin-catalog-item" key={plugin.id} aria-label={plugin.name}>
     <div className="plugin-card-heading"><span className="plugin-card-symbol" aria-hidden="true"><Icon size={23} /></span><Tag color={plugin.source === 'official' ? 'blue' : 'default'}>{sourceLabels[plugin.source]}</Tag></div>
     <h3><button type="button" onClick={() => setSelected(plugin)}>{plugin.name}</button></h3>
     <p className="plugin-card-description">{plugin.summary}</p>
     <div className="plugin-card-metadata"><span>{plugin.category}</span><span>{plugin.author}</span></div>
     <div className="plugin-card-footer"><span title="收录版本">v{plugin.version}</span><Button onClick={() => setSelected(plugin)}>查看详情</Button></div>
    </article>;
   })}
  </div>
  {!items.length && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有符合条件的插件"><Button onClick={() => { setSearch(''); setCategory('all'); setSource('all'); }}>清除筛选</Button></Empty>}
  <DataPagination label="插件" pagination={pagination} count={items.length} total={matches.length} loading={false} failed={false} />
  {selected && <PluginDetails key={selected.id} plugin={selected} onClose={() => setSelected(null)} />}
 </section>;
}

function PluginDetails({ plugin, onClose }: { plugin: CatalogPlugin; onClose: () => void }) {
 const Icon = pluginIcons[plugin.id] ?? Puzzle;
 const [copied, setCopied] = useState(false);
 const [copying, setCopying] = useState(false);
 const [copyError, setCopyError] = useState(false);
 const spec = plugin.packageName + '@' + plugin.version;
 const copy = async () => {
  setCopying(true); setCopyError(false);
  try { await navigator.clipboard.writeText(spec); setCopied(true); }
  catch { setCopied(false); setCopyError(true); }
  finally { setCopying(false); }
 };
 return <Drawer className="plugin-detail-drawer" title="插件详情" width={620} open onClose={onClose}>
  <div className="plugin-detail-heading"><span className="plugin-card-symbol" aria-hidden="true"><Icon size={25} /></span><div><h2>{plugin.name}</h2><Tag color={plugin.source === 'official' ? 'blue' : 'default'}>{sourceLabels[plugin.source]}</Tag><span className="plugin-detail-category">{plugin.category}</span></div></div>
  <p className="plugin-detail-description">{plugin.description}</p>
  <Descriptions size="small" column={1} items={[
   { key: 'author', label: '发布方', children: plugin.author },
   { key: 'version', label: '收录版本', children: plugin.version },
   { key: 'compatibility', label: '版本要求', children: plugin.compatibility },
   { key: 'checked', label: '来源核对', children: catalogCheckedAt + ' · 包信息及 DSH bundle 声明' },
  ]} />
  <div className="plugin-detail-links"><a href={plugin.documentation} target="_blank" rel="noopener noreferrer">使用文档<ExternalLink size={14} aria-hidden="true" /></a><a href={plugin.repository} target="_blank" rel="noopener noreferrer">源代码<ExternalLink size={14} aria-hidden="true" /></a><a href={'https://www.npmjs.com/package/' + plugin.packageName + '/v/' + plugin.version} target="_blank" rel="noopener noreferrer">npm 包<ExternalLink size={14} aria-hidden="true" /></a></div>
  <section className="plugin-install-guide" aria-label="使用方式"><h3>在 DSH 中使用</h3>
   {plugin.bundled ? <p>此官方组合包随对应版本的 DSH 提供，可先在 DSH“插件”页查找并启用。实际可用项以所用版本为准。</p> : <ol><li>复制下方包名和版本。</li><li>在 DSH“插件”页选择“添加插件”，粘贴包名。</li><li>查看版本和依赖检查结果，按原生提示确认后安装。</li></ol>}
   <div className="plugin-install-spec"><code tabIndex={0} aria-label="插件安装包名">{spec}</code><Button icon={copied ? <Check size={15} aria-hidden="true" /> : <Copy size={15} aria-hidden="true" />} loading={copying} onClick={() => void copy()}>{copied ? '已复制' : '复制包名'}</Button></div>
   <span className="plugin-copy-status" role="status" aria-live="polite">{copied ? '包名与版本已复制' : ''}</span>
   {copyError && <Alert type="warning" showIcon message="复制失败，请手动选择并复制上方包名" />}
   <p className="plugin-install-note">安装与启用由你在 DSH 中完成，插件仅提供目录和说明。</p>
  </section>
  <Alert type="info" showIcon message="收录不代表已通过运行验证" description="来源核对仅确认公开包信息和组合包声明。实际兼容性、依赖及网络要求以所用 DSH 版本和插件文档为准。" />
 </Drawer>;
}

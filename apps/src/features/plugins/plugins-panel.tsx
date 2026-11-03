import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Empty, Input, Select, Spin, Tag } from 'antd';
import { Plus, Search } from 'lucide-react';
import { useState } from 'react';
import { hasPermission, type CurrentUser } from '../../api/identity';
import { DataPagination, usePagePagination } from '../../shared/components/data-pagination';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';
import { errorMessage } from '../../shared/utils/error-message';
import {
  filterPlugins,
  isBundledMem0Plugin,
  pluginCategories,
  type CatalogPlugin,
} from './plugin-catalog';
import './plugins.css';

import { client } from './plugin-client';

import { iconForPlugin } from './plugin-icons';

import { AddPlugin } from './add-plugin';

import { PluginDetails } from './plugin-details';

export function PluginsPanel({ user }: { user: CurrentUser }) {
  const cache = useQueryClient();
  const list = useQuery({
    queryKey: ['plugins', user.id],
    queryFn: ({ signal }) => client.list(user, signal),
    retry: false,
    refetchOnWindowFocus: false,
  });
  useExpiredSession(list.error);
  const [adding, setAdding] = useState(false);
  const [notice, setNotice] = useState('');
  const [search, setSearch] = useState('');
  const [category, setCategory] = useState('all');
  const [selected, setSelected] = useState<CatalogPlugin | null>(null);
  const pagination = usePagePagination(JSON.stringify([search, category]));
  const catalog = list.data?.items ?? [];
  const activePlugin =
    selected && catalog.find((plugin) => plugin.packageName === selected.packageName);
  const matches = filterPlugins(search, category, catalog);
  const items = matches.slice(
    (pagination.page - 1) * pagination.pageSize,
    pagination.page * pagination.pageSize,
  );
  return (
    <section className="plugins-panel" aria-label="插件">
      <div className="provider-page-heading">
        <div>
          <h2>发现插件</h2>
          <p>发现扩展，让工作空间拥有更多能力。</p>
        </div>
        {hasPermission(user, 'plugins:manage') && (
          <Button
            type="primary"
            icon={<Plus size={16} aria-hidden="true" />}
            disabled={list.isPending || list.isError}
            onClick={() => {
              setNotice('');
              setAdding(true);
            }}
          >
            添加插件
          </Button>
        )}
      </div>
      {list.isError && (
        <Alert
          type="error"
          showIcon
          message="插件列表暂时无法加载"
          description={errorMessage(list.error)}
          action={
            <Button loading={list.isFetching} onClick={() => void list.refetch()}>
              重试
            </Button>
          }
        />
      )}
      {notice && (
        <Alert
          role="status"
          type="success"
          showIcon
          message={notice}
          closable
          onClose={() => setNotice('')}
        />
      )}
      <div className="plugin-catalog-toolbar">
        <Input
          role="searchbox"
          aria-label="搜索插件"
          placeholder="搜索名称、功能、包名或作者"
          prefix={<Search size={16} aria-hidden="true" />}
          value={search}
          onChange={(event) => setSearch(event.target.value)}
          allowClear
        />
        <div className="plugin-catalog-filters">
          <label>
            <span>分类</span>
            <Select
              aria-label="插件分类"
              value={category}
              onChange={setCategory}
              options={[
                { value: 'all', label: '全部分类' },
                ...pluginCategories.map((value) => ({ value, label: value })),
              ]}
            />
          </label>
        </div>
      </div>
      <Spin spinning={list.isPending}>
        <div className="plugin-catalog-grid" aria-label="插件目录">
          {items.map((plugin) => {
            const Icon = iconForPlugin(plugin);
            return (
              <article className="plugin-catalog-item" key={plugin.id} aria-label={plugin.name}>
                <div className="plugin-card-heading">
                  <span className="plugin-card-symbol" aria-hidden="true">
                    <Icon size={23} />
                  </span>
                  <div className="plugin-card-tags">
                    <Tag>{isBundledMem0Plugin(plugin) ? '本地维护' : '第三方'}</Tag>
                    {plugin.additionMethod === 'offline' && <Tag>离线</Tag>}
                  </div>
                </div>
                <h3>
                  <button type="button" onClick={() => setSelected(plugin)}>
                    {plugin.name}
                  </button>
                </h3>
                <p className="plugin-card-description">{plugin.summary}</p>
                <div className="plugin-card-metadata">
                  <span>{plugin.category}</span>
                  <span>{plugin.author}</span>
                </div>
                <div className="plugin-card-footer">
                  <span title="收录版本">v{plugin.version}</span>
                  <Button onClick={() => setSelected(plugin)}>查看详情</Button>
                </div>
              </article>
            );
          })}
        </div>
      </Spin>
      {!list.isPending && !list.isError && !items.length && (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有符合条件的插件">
          <Button
            onClick={() => {
              setSearch('');
              setCategory('all');
            }}
          >
            清除筛选
          </Button>
        </Empty>
      )}
      <DataPagination
        label="插件"
        pagination={pagination}
        count={items.length}
        total={matches.length}
        loading={list.isFetching}
        failed={false}
      />
      {adding && (
        <AddPlugin
          user={user}
          onClose={() => setAdding(false)}
          onAdded={(plugin) => {
            cache.setQueryData<{ items: CatalogPlugin[] }>(['plugins', user.id], (current) => ({
              items: [
                plugin,
                ...(current?.items ?? []).filter((item) => item.packageName !== plugin.packageName),
              ],
            }));
            setAdding(false);
            setSearch('');
            setCategory('all');
            pagination.go(1);
            setNotice('插件已添加');
          }}
        />
      )}
      {activePlugin && (
        <PluginDetails
          key={activePlugin.packageName}
          user={user}
          plugin={activePlugin}
          onClose={() => setSelected(null)}
          onSaved={(plugin) => {
            cache.setQueryData<{ items: CatalogPlugin[] }>(['plugins', user.id], (current) => ({
              items: (current?.items ?? []).map((item) =>
                item.packageName === plugin.packageName ? plugin : item,
              ),
            }));
            setNotice('插件信息已保存');
          }}
        />
      )}
    </section>
  );
}

export { pluginIcons } from './plugin-icons';

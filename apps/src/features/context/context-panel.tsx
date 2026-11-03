import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Dropdown,
  Empty,
  Form,
  Input,
  Modal,
  Segmented,
  Select,
  Spin,
  Switch,
  Tag,
} from 'antd';
import { ArrowLeft, Brain, Ellipsis, Library, Plus } from 'lucide-react';
import { useState } from 'react';
import { type ContextConnection, type ContextKind } from '../../api/context';
import { APIError } from '../../api/error';
import { hasPermission, type CurrentUser } from '../../api/identity';
import { DataPagination, usePagePagination } from '../../shared/components/data-pagination';
import { DeleteConfirmation } from '../../shared/components/delete-confirmation';
import { ListSearch } from '../../shared/components/list-search';
import { expireSession, useExpiredSession } from '../../shared/hooks/use-expired-session';
import { errorMessage as message } from '../../shared/utils/error-message';
import {
  ContextServiceIcon,
  ContextServicePicker,
  ContextServiceSummary,
} from './context-service-picker';
import './context.css';
import { EmbeddingFields, externalEmbedding, legacyEmbedding } from './embedding-fields';
import './knowledge-management.css';

import { backendNames, client, date, memoryCustomBackends, status } from './context-shared';

import { ContextDetails } from './context-details';

export function ContextPanel({ user, kind }: { user: CurrentUser; kind: ContextKind }) {
  const memory = kind === 'memory';
  const label = memory ? '记忆' : '知识库';
  const manage = hasPermission(user, kind + ':manage');
  const cache = useQueryClient();
  const list = useQuery({
    queryKey: ['context', user.id, kind],
    queryFn: ({ signal }) => client.list(user, kind, signal),
    retry: false,
  });
  useExpiredSession(list.error);
  const [editor, setEditor] = useState<ContextConnection | 'new' | null>(null);
  const [selected, setSelected] = useState<ContextConnection | null>(null);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [form] = Form.useForm();
  const [pickerOpen, setPickerOpen] = useState(false);
  const [backend, setBackend] = useState('');
  const [deleting, setDeleting] = useState<ContextConnection | null>(null);
  const [confirm, setConfirm] = useState('');
  const [search, setSearch] = useState('');
  const [filter, setFilter] = useState('全部');
  const pagination = usePagePagination(JSON.stringify([user.id, kind, search, filter]));
  async function run(id: string, task: () => Promise<void>) {
    setBusy(id);
    setError('');
    try {
      await task();
      await cache.invalidateQueries({ queryKey: ['context', user.id, kind] });
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 401) expireSession(cache);
      setError(message(cause));
    } finally {
      setBusy('');
    }
  }
  function edit(connection: ContextConnection | 'new', service = '') {
    const chosen = connection === 'new' ? service : connection.backend;
    setError('');
    setBackend(chosen);
    form.resetFields();
    const custom = memoryCustomBackends.includes(chosen);
    const available =
      list.data?.endpoints.filter(
        (e) =>
          e.backend === chosen &&
          (!(['milvus', 'weaviate', 'qdrant'].includes(chosen) || custom) ||
            connection !== 'new' ||
            e.custom_service),
      ) ?? [];
    form.setFieldsValue(
      connection === 'new'
        ? {
            enabled: true,
            name: `${backendNames[chosen] ?? chosen} ${label}`,
            endpoint_id: available.length === 1 ? available[0].id : undefined,
            ...(!memory ? { embedding: { ...externalEmbedding } } : {}),
          }
        : {
            ...connection,
            ...(!memory
              ? { embedding: { ...(connection.embedding ?? legacyEmbedding), api_key: '' } }
              : {}),
            credential: '',
          },
    );
    setEditor(connection);
  }
  const customService =
    (['milvus', 'weaviate', 'qdrant'].includes(backend) ||
      memoryCustomBackends.includes(backend)) &&
    (editor === 'new' || (editor !== null && !!editor.service_url));
  const endpoints =
    list.data?.endpoints.filter(
      (e) => e.backend === backend && (!customService || e.custom_service),
    ) ?? [];
  const items = list.isError ? [] : (list.data?.items ?? []);
  const filtered = items.filter(
    (v) =>
      (filter === '全部' || v.enabled === (filter === '已启用')) &&
      (v.name + ' ' + (backendNames[v.backend] ?? v.backend))
        .toLocaleLowerCase()
        .includes(search.trim().toLocaleLowerCase()),
  );
  const visible = filtered.slice(
    (pagination.page - 1) * pagination.pageSize,
    pagination.page * pagination.pageSize,
  );
  const Symbol = memory ? Brain : Library;
  return (
    <section
      className={`instances-section context-panel${memory ? '' : ' knowledge-panel'}`}
      aria-label={`${label}管理`}
    >
      <div className="provider-page-heading workspace-actions">
        <div className="knowledge-page-heading">
          <h2>{label}</h2>
          <p>
            {memory
              ? '维护记忆、检索经验，供接入的工作空间共用。'
              : '维护文档、检索知识，供接入的工作空间共用。'}
          </p>
        </div>
        {manage && (
          <Button
            type="primary"
            icon={<Plus size={16} aria-hidden="true" />}
            disabled={!list.data || list.isError}
            onClick={() => setPickerOpen(true)}
          >{`添加${label}`}</Button>
        )}
      </div>
      {(memory || items.length > 0) && (
        <div className="workspace-toolbar">
          <Segmented
            aria-label={`${label}连接状态筛选`}
            options={['全部', '已启用', '已停用']}
            value={filter}
            onChange={(value) => setFilter(String(value))}
          />
          <ListSearch
            label={`搜索${label}连接`}
            placeholder="搜索名称或方案"
            value={search}
            onChange={setSearch}
            refreshLabel={`刷新${label}`}
            loading={list.isFetching}
            onRefresh={() => void list.refetch()}
          />
        </div>
      )}
      {(error || list.isError) && (
        <Alert
          type="error"
          showIcon
          message={error || message(list.error)}
          action={
            list.isError ? <Button onClick={() => void list.refetch()}>重试</Button> : undefined
          }
        />
      )}
      <Spin spinning={list.isPending}>
        <div className="workspace-list" aria-label={`${label}连接列表`}>
          {visible.length ? (
            visible.map((v) => {
              const state = v.enabled ? status[v.status] : { label: '已停用', color: 'default' };
              return (
                <article
                  className="workspace-record"
                  key={v.id}
                  aria-label={`${label}连接 ${v.name}`}
                >
                  <span className="workspace-symbol" aria-hidden="true">
                    <ContextServiceIcon backend={v.backend} size={24} />
                  </span>
                  <div className="workspace-record-content">
                    <div className="workspace-record-heading">
                      <h3>
                        <Button type="link" onClick={() => setSelected(v)}>
                          {v.name}
                        </Button>
                      </h3>
                      <Tag color={state.color}>{state.label}</Tag>
                    </div>
                    <div className="workspace-record-meta">
                      <span>{backendNames[v.backend] ?? v.backend}</span>
                      {!memory && v.service_url && (
                        <span className="knowledge-service-address" title={v.service_url}>
                          {v.service_url}
                        </span>
                      )}
                      <span>最近验证：{v.verified_at ? date(v.verified_at) : '—'}</span>
                    </div>
                    {!memory && v.embedding && (
                      <p className="knowledge-connection-model">
                        Embedding · {v.embedding.model} <span>{v.embedding.dimensions} 维</span>
                      </p>
                    )}
                  </div>
                  <div className="workbench-record-actions">
                    <Button className="workspace-details" onClick={() => setSelected(v)}>
                      管理内容
                    </Button>
                    {manage && (
                      <>
                        <Button
                          loading={busy === v.id}
                          disabled={!v.enabled || !!busy}
                          onClick={() =>
                            void run(v.id, async () => {
                              await client.call(user, v, 'verify');
                            })
                          }
                        >
                          验证
                        </Button>
                        <Dropdown
                          trigger={['click']}
                          menu={{
                            items: [
                              { key: 'settings', label: '设置' },
                              { key: 'remove', label: '移除', danger: true },
                            ],
                            onClick: ({ key }) => {
                              if (key === 'settings') edit(v);
                              else if (key === 'remove') {
                                setDeleting(v);
                                setConfirm('');
                              }
                            },
                          }}
                        >
                          <Button
                            aria-label={`${v.name} 更多操作`}
                            disabled={!!busy}
                            icon={<Ellipsis size={17} aria-hidden="true" />}
                          />
                        </Dropdown>
                      </>
                    )}
                  </div>
                </article>
              );
            })
          ) : (
            <div className="workspace-list-empty">
              {!memory && (
                <span className="knowledge-empty-icon">
                  <Symbol size={28} aria-hidden="true" />
                </span>
              )}
              <Empty
                image={memory ? Empty.PRESENTED_IMAGE_SIMPLE : false}
                description={
                  list.isError
                    ? `未能获取${label}连接`
                    : search || filter !== '全部'
                      ? `没有符合条件的${label}连接`
                      : `暂无${label}连接`
                }
              />
              {!memory && !list.isError && !list.isPending && (
                <>
                  {search || filter !== '全部' ? (
                    <Button
                      onClick={() => {
                        setSearch('');
                        setFilter('全部');
                      }}
                    >
                      清除筛选
                    </Button>
                  ) : (
                    <>
                      <p>连接知识库后，添加文档并通过检索查找相关内容。</p>
                      {manage && (
                        <Button disabled={!list.data} onClick={() => setPickerOpen(true)}>
                          连接第一个知识库
                        </Button>
                      )}
                    </>
                  )}
                </>
              )}
            </div>
          )}
        </div>
      </Spin>
      {(memory || items.length > 0) && (
        <DataPagination
          label={`${label}连接`}
          pagination={pagination}
          count={visible.length}
          total={filtered.length}
          loading={list.isFetching}
          failed={list.isError}
        />
      )}
      {pickerOpen && (
        <ContextServicePicker
          kind={kind}
          endpoints={list.data?.endpoints ?? []}
          onClose={() => setPickerOpen(false)}
          onSelect={(service) => {
            setPickerOpen(false);
            edit('new', service);
          }}
        />
      )}
      <Modal
        className="context-connection-editor context-knowledge-editor"
        width={600}
        open={editor !== null}
        title={editor === 'new' ? `添加${label}连接` : '连接设置'}
        okText="保存"
        cancelText="取消"
        confirmLoading={busy === 'save'}
        okButtonProps={{
          disabled: endpoints.length === 0,
          'aria-label': '保存',
          'aria-busy': busy === 'save',
        }}
        afterOpenChange={(open) => {
          if (open && endpoints.length) form.focusField('name');
        }}
        onCancel={() => {
          if (!busy) setEditor(null);
        }}
        onOk={() => {
          if (!busy) form.submit();
        }}
      >
        <div className="context-editor-heading">
          {editor === 'new' && (
            <Button
              className="context-editor-back"
              type="text"
              icon={<ArrowLeft size={15} aria-hidden="true" />}
              disabled={!!busy}
              onClick={() => {
                setEditor(null);
                setPickerOpen(true);
              }}
            >
              返回选择
            </Button>
          )}
          <ContextServiceSummary backend={backend} compact />
        </div>
        {error && <Alert type="error" showIcon message={error} />}{' '}
        {endpoints.length === 0 && backend !== 'openviking' && (
          <Alert
            type="info"
            showIcon
            message="暂未接入"
            description={
              memoryCustomBackends.includes(backend)
                ? `请联系管理员配置支持自定义地址的 ${backendNames[backend]} 接入节点后保存。`
                : '请联系管理员完成配置后使用。'
            }
          />
        )}{' '}
        {(endpoints.length > 0 || (memoryCustomBackends.includes(backend) && editor === 'new')) && (
          <Form
            form={form}
            name="context-connection"
            layout="vertical"
            disabled={!!busy}
            scrollToFirstError={{ block: 'center', behavior: 'instant', focus: true }}
            onFinish={(values) =>
              void run('save', async () => {
                await client.request(
                  user,
                  kind,
                  editor === 'new' ? '' : `/${(editor as ContextConnection).id}`,
                  {
                    ...values,
                    expected_version: editor === 'new' ? 0 : (editor as ContextConnection).version,
                  },
                  editor === 'new' ? 'POST' : 'PUT',
                );
                setEditor(null);
              })
            }
          >
            <Form.Item
              name="name"
              label="连接名称"
              rules={[
                {
                  required: true,
                  whitespace: true,
                  max: 100,
                  message: '请输入 1～100 个字符的连接名称',
                },
              ]}
            >
              <Input maxLength={100} placeholder={`例如：项目${label}`} />
            </Form.Item>
            <Form.Item
              name="endpoint_id"
              hidden={customService && endpoints.length <= 1}
              label={customService ? '接入节点' : '服务'}
              rules={[{ required: true, message: '请选择服务' }]}
            >
              <Select
                disabled={editor !== 'new' || !!busy}
                virtual={false}
                options={endpoints.map((e) => ({ value: e.id, label: e.name }))}
                notFoundContent="暂无可用服务"
              />
            </Form.Item>
            {customService && (
              <Form.Item
                name="service_url"
                label={
                  backend === 'graphiti' ? 'Neo4j Bolt 地址' : `${backendNames[backend]} 服务地址`
                }
                rules={[
                  {
                    required: true,
                    whitespace: true,
                    message:
                      backend === 'graphiti'
                        ? '请输入 Neo4j Bolt 地址'
                        : `请输入 ${backendNames[backend]} 服务地址`,
                  },
                  {
                    validator: (_, value: string) => {
                      if (!value) return Promise.resolve();
                      try {
                        const url = new URL(value);
                        if (
                          (backend === 'graphiti'
                            ? ['bolt:', 'bolt+s:']
                            : ['http:', 'https:']
                          ).includes(url.protocol) &&
                          !url.username &&
                          !url.password &&
                          !url.search &&
                          !url.hash &&
                          (url.pathname === '' || url.pathname === '/')
                        )
                          return Promise.resolve();
                      } catch {}
                      return Promise.reject(
                        new Error(
                          backend === 'graphiti'
                            ? '填写 bolt+s:// 或 bolt:// 地址，不要包含路径、参数或凭据'
                            : '填写 http:// 或 https:// 地址，不要包含路径、参数或凭据',
                        ),
                      );
                    },
                  },
                ]}
              >
                <Input
                  placeholder={
                    backend === 'graphiti'
                      ? 'bolt+s://neo4j.example.com:7687'
                      : `https://your-${backend}.example.com`
                  }
                  maxLength={1000}
                  readOnly={editor !== 'new'}
                  autoComplete="url"
                />
              </Form.Item>
            )}
            {memory && !customService && (
              <Form.Item noStyle shouldUpdate>
                {() => {
                  const e = list.data?.endpoints.find(
                    (e) => e.id === form.getFieldValue('endpoint_id'),
                  );
                  return e ? <p className="context-note">{e.data_use}</p> : null;
                }}
              </Form.Item>
            )}
            <Form.Item
              name="credential"
              label="服务凭据"
              extra={
                memory
                  ? customService
                    ? backend === 'graphiti'
                      ? 'Neo4j 用户名:密码；使用接入节点配置的模型。'
                      : backend === 'openviking'
                        ? 'OpenViking 管理员 API Key，凭据加密保存，不会读回。'
                        : 'Mem0 API Key，凭据加密保存，不会读回。'
                    : editor === 'new'
                      ? '凭据加密保存，不会读回。'
                      : '留空保留原凭据；保存后需重新验证。'
                  : undefined
              }
              rules={[
                ...(editor === 'new' && (!customService || memory)
                  ? [
                      {
                        required: true,
                        whitespace: true,
                        message:
                          backend === 'graphiti' && customService
                            ? '请输入 Neo4j 用户名:密码'
                            : memory && customService
                              ? '请输入服务 API Key'
                              : '请输入服务凭据',
                      },
                    ]
                  : []),
                ...(backend === 'graphiti' && customService
                  ? [
                      {
                        validator: (_: unknown, value: string) =>
                          !value || /^[^:\s][^:]*:.+/.test(value)
                            ? Promise.resolve()
                            : Promise.reject(new Error('请输入 Neo4j 用户名:密码')),
                      },
                    ]
                  : []),
              ]}
            >
              <Input.Password
                placeholder={
                  editor !== 'new'
                    ? '留空保留已保存的凭据'
                    : customService
                      ? backend === 'graphiti'
                        ? '用户名:密码'
                        : backend === 'openviking'
                          ? '管理员 API Key'
                          : backend === 'mem0'
                            ? 'API Key'
                            : backend === 'milvus'
                              ? '选填，Token 或 用户名:密码'
                              : '选填，API Key'
                      : undefined
                }
                autoComplete="new-password"
                maxLength={4096}
              />
            </Form.Item>
            {!memory && editor !== null && <EmbeddingFields connection={editor} />}
            <p className="context-note">同一{label}的内容由接入它的工作空间共用。</p>
            <div className="context-editor-enabled">
              <label htmlFor="context-connection-enabled">启用连接</label>
              <Form.Item name="enabled" valuePropName="checked">
                <Switch id="context-connection-enabled" />
              </Form.Item>
            </div>
            {memory && customService && (
              <details className="context-connection-help">
                <summary>连接说明</summary>
                <ul>
                  <li>
                    {backend === 'mem0'
                      ? '填写平台接入服务可访问的 Mem0 OSS REST 地址及 API Key。'
                      : backend === 'openviking'
                        ? '填写平台接入服务可访问的 OpenViking 地址及管理员 API Key。'
                        : '填写平台接入服务可访问的 Neo4j Bolt 地址和用户名:密码。'}
                  </li>
                  <li>凭据加密保存，不会回显；编辑时留空可保留已有凭据。</li>
                  {backend === 'graphiti' && (
                    <li>图谱提取使用接入节点配置的模型，记忆正文会发送到该模型。</li>
                  )}
                  <li>服务地址保存后不可更改，更换服务请新建连接。</li>
                </ul>
              </details>
            )}
            {!memory && (
              <details className="context-connection-help">
                <summary>连接说明</summary>
                <ul>
                  {customService && (
                    <li>
                      填写平台可访问的 {backendNames[backend]} 服务地址。
                      {backend === 'milvus'
                        ? '凭据支持 Token 或用户名:密码。'
                        : '凭据使用服务的 API Key。'}
                      未启用认证可留空。
                    </li>
                  )}
                  <li>凭据加密保存，不会回显；留空保留已有凭据。</li>
                  <li>原文保存在平台接入服务，向量写入所选数据库。</li>
                  <li>
                    Embedding 模型通过 API
                    接入，支持本机、私网自建服务或云端服务。填写平台接入服务可访问的地址，本机服务请使用局域网地址。
                  </li>
                  <li>向量维度须与模型输出一致；云端 API 费用按服务商规则计算。</li>
                  {editor !== 'new' && (
                    <li>更换服务地址或 Embedding 模型需新建连接并重新导入文档。</li>
                  )}
                </ul>
              </details>
            )}
          </Form>
        )}
      </Modal>
      <DeleteConfirmation
        open={!!deleting}
        title="移除外部连接？"
        name={deleting?.name ?? ''}
        label="确认连接名称"
        value={confirm}
        onChange={setConfirm}
        confirmText="移除连接"
        busy={!!busy}
        error={error}
        onCancel={() => setDeleting(null)}
        onConfirm={() =>
          deleting &&
          void run('remove', async () => {
            await client.request(
              user,
              kind,
              `/${deleting.id}`,
              { expected_version: deleting.version, confirmed_name: confirm },
              'DELETE',
            );
            setDeleting(null);
          })
        }
      >
        <p>
          解除此连接及全部工作空间绑定，<strong>外部{label}数据将保留</strong>。
        </p>
      </DeleteConfirmation>
      {selected && (
        <ContextDetails
          key={selected.id}
          user={user}
          connection={items.find((v) => v.id === selected.id) ?? selected}
          onClose={() => setSelected(null)}
        />
      )}
    </section>
  );
}

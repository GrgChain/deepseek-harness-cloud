import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Checkbox,
  Dropdown,
  Empty,
  Form,
  Input,
  Modal,
  Space,
  Spin,
  Tooltip,
} from 'antd';
import { BookOpen, Download, Ellipsis, Plus, Upload } from 'lucide-react';
import { useEffect, useState } from 'react';
import { APIError } from '../../api/error';
import { hasPermission, type CurrentUser } from '../../api/identity';
import { type Skill, type SkillPreview } from '../../api/skills';
import { DataPagination, usePagePagination } from '../../shared/components/data-pagination';
import { DeleteConfirmation } from '../../shared/components/delete-confirmation';
import { ListSearch } from '../../shared/components/list-search';
import { expireSession, useExpiredSession } from '../../shared/hooks/use-expired-session';
import { errorMessage } from '../../shared/utils/error-message';

import { client, date, download, size } from './skill-shared';

import { SkillEditor } from './skill-editor';

import { ImportDialog } from './skill-import-dialog';

import { ExportDialog } from './skill-export-dialog';

export function SkillsPanel({ user }: { user: CurrentUser }) {
  const [search, setSearch] = useState('');
  const [filter, setFilter] = useState('');
  const [selected, setSelected] = useState<string[]>([]);
  const [editor, setEditor] = useState<string | null>(null);
  const [create, setCreate] = useState(false);
  const [form] = Form.useForm<{ name: string; description: string }>();
  const [importDialog, setImportDialog] = useState<{
    initial: 'search' | 'file' | 'source';
    preview?: SkillPreview;
  } | null>(null);
  const [exports, setExports] = useState<string[] | null>(null);
  const [deleting, setDeleting] = useState<Skill | null>(null);
  const [confirmation, setConfirmation] = useState('');
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState('');
  const cache = useQueryClient();
  const manage = hasPermission(user, 'skills:manage');
  useEffect(() => {
    const timer = setTimeout(() => setFilter(search.trim()), 250);
    return () => clearTimeout(timer);
  }, [search]);
  const pagination = usePagePagination([user.id, filter].join(':'));
  const list = useQuery({
    queryKey: ['skills', user.id, 'list', filter, pagination.page, pagination.pageSize],
    queryFn: ({ signal }) =>
      client.list(user, filter, pagination.page, pagination.pageSize, signal),
    retry: false,
  });
  useExpiredSession(list.error);
  useEffect(() => {
    setSelected([]);
  }, [filter, pagination.page, pagination.pageSize]);
  useEffect(() => {
    if (notice) {
      const timer = setTimeout(() => setNotice(''), 5000);
      return () => clearTimeout(timer);
    }
  }, [notice]);
  const items = list.isError ? [] : (list.data?.items ?? []);
  async function refresh() {
    await cache.invalidateQueries({ queryKey: ['skills', user.id] });
  }
  async function action(key: string, run: () => Promise<void>) {
    setBusy(key);
    setError('');
    try {
      await run();
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 401) expireSession(cache);
      setError(errorMessage(cause));
    } finally {
      setBusy('');
    }
  }
  function select(id: string, checked: boolean) {
    setSelected((current) =>
      checked ? [...current, id] : current.filter((value) => value !== id),
    );
  }
  const all = items.length > 0 && items.every((item) => selected.includes(item.id));
  return (
    <section className="instances-section skills-panel" aria-label="技能包">
      <div className="provider-page-heading workspace-actions">
        <h2>技能包</h2>
        {manage && (
          <Space wrap>
            <Button
              aria-label="导入"
              icon={<Upload size={16} aria-hidden="true" />}
              onClick={() => {
                setError('');
                setImportDialog({ initial: 'search' });
              }}
            >
              导入
            </Button>
            <Button
              type="primary"
              icon={<Plus size={16} aria-hidden="true" />}
              onClick={() => {
                setError('');
                form.resetFields();
                setCreate(true);
              }}
            >
              新建技能
            </Button>
          </Space>
        )}
      </div>
      <div className="workspace-toolbar">
        <Space className="skill-selection" wrap>
          <Checkbox
            aria-label="选择本页全部 Skills"
            checked={all}
            disabled={!items.length}
            indeterminate={selected.length > 0 && !all}
            onChange={(event) =>
              setSelected(event.target.checked ? items.map((item) => item.id) : [])
            }
          >
            选择本页
          </Checkbox>
          <Button
            icon={<Download size={16} aria-hidden="true" />}
            disabled={!selected.length || selected.length > 50}
            onClick={() => setExports(selected)}
          >
            导出{selected.length ? ` (${selected.length})` : ''}
          </Button>
        </Space>
        <ListSearch
          label="搜索 Skills"
          placeholder="搜索名称或说明"
          value={search}
          onChange={setSearch}
          onSubmit={() => setFilter(search.trim())}
          onRefresh={() => void list.refetch()}
          loading={list.isFetching}
          refreshLabel="刷新 Skills"
        />
      </div>
      {notice && (
        <Alert type="success" showIcon message={notice} closable onClose={() => setNotice('')} />
      )}
      {(error || list.isError) && (
        <Alert
          type="error"
          showIcon
          message={error || list.error?.message}
          closable={!!error}
          onClose={() => setError('')}
          action={
            list.isError ? <Button onClick={() => void list.refetch()}>重试</Button> : undefined
          }
        />
      )}
      <Spin spinning={list.isPending}>
        <div className="workspace-list" aria-label="技能包列表">
          {items.length ? (
            items.map((skill) => (
              <article
                className="workspace-record skill-row"
                key={skill.id}
                aria-label={`Skill ${skill.name}`}
              >
                <Checkbox
                  aria-label={`选择 ${skill.name}`}
                  checked={selected.includes(skill.id)}
                  onChange={(event) => select(skill.id, event.target.checked)}
                />
                <span className="workspace-symbol" aria-hidden="true">
                  <BookOpen size={20} />
                </span>
                <div className="workspace-record-content skill-identity">
                  <div className="workspace-record-heading">
                    <h3>
                      <Button type="link" onClick={() => setEditor(skill.id)}>
                        {skill.name}
                      </Button>
                    </h3>
                  </div>
                  <p title={skill.description}>{skill.description}</p>
                  <div className="workspace-record-meta">
                    <span>
                      {skill.file_count} 个文件 · {size(skill.size_bytes)}
                    </span>
                    <span>
                      {skill.source_url ? (
                        <Tooltip title={skill.source_url}>
                          <span>外部链接</span>
                        </Tooltip>
                      ) : (
                        '个人技能'
                      )}
                    </span>
                    <time dateTime={skill.updated_at}>更新于 {date(skill.updated_at)}</time>
                  </div>
                </div>
                <div className="skill-row-actions">
                  <Tooltip title="下载 Skill">
                    <Button
                      aria-label={`下载 ${skill.name}`}
                      icon={<Download size={16} />}
                      onClick={() => setExports([skill.id])}
                    />
                  </Tooltip>
                  <Dropdown
                    trigger={['click']}
                    menu={{
                      items: [
                        { key: 'edit', label: manage ? '编辑' : '查看' },
                        ...(manage && skill.source_url ? [{ key: 'sync', label: '检查更新' }] : []),
                        ...(skill.has_previous
                          ? [{ key: 'previous', label: '下载替换前版本' }]
                          : []),
                        ...(manage ? [{ key: 'delete', label: '删除', danger: true }] : []),
                      ],
                      onClick: ({ key }) => {
                        if (key === 'edit') setEditor(skill.id);
                        else if (key === 'delete') {
                          setError('');
                          setDeleting(skill);
                          setConfirmation('');
                        } else if (key === 'sync')
                          void action(skill.id, async () => {
                            const preview = await client.check(user, skill);
                            if (preview.unchanged) setNotice('此 Skill 已是最新内容');
                            else setImportDialog({ initial: 'source', preview });
                          });
                        else if (key === 'previous')
                          void action(skill.id, async () => {
                            const file = await client.download(user, [skill.id], true);
                            download(file.blob, file.name);
                          });
                      },
                    }}
                  >
                    <Button
                      aria-label={`${skill.name} 更多操作`}
                      disabled={!!busy}
                      icon={<Ellipsis size={17} />}
                    />
                  </Dropdown>
                </div>
              </article>
            ))
          ) : (
            <div className="workspace-list-empty">
              <BookOpen size={28} aria-hidden="true" />
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={
                  list.isError ? '技能库暂不可用' : filter ? '没有匹配的 Skill' : '暂无技能包'
                }
              />
            </div>
          )}
        </div>
      </Spin>
      <DataPagination
        label="Skills"
        pagination={pagination}
        count={list.isError ? 0 : items.length}
        total={list.isError ? undefined : list.data?.total}
        loading={list.isFetching}
        failed={list.isError}
      />
      {editor && (
        <SkillEditor
          key={editor}
          user={user}
          id={editor}
          onClose={() => setEditor(null)}
          onSaved={() => {
            setEditor(null);
            setNotice('Skill 已保存');
            void refresh();
          }}
        />
      )}
      <Modal
        open={create}
        title="新建技能"
        okText="创建并编辑"
        confirmLoading={busy === 'create'}
        cancelButtonProps={{ disabled: !!busy }}
        maskClosable={!busy}
        closable={!busy}
        keyboard={!busy}
        onCancel={() => setCreate(false)}
        onOk={() => form.submit()}
      >
        <Form
          form={form}
          layout="vertical"
          onFinish={(values) =>
            void action('create', async () => {
              const skill = await client.create(user, values.name, values.description);
              setCreate(false);
              setEditor(skill.id);
              await refresh();
            })
          }
        >
          <Form.Item
            name="name"
            label="Skill 名称"
            rules={[
              { required: true, message: '请输入名称' },
              {
                validator: (_, value: string) =>
                  !value ||
                  (/^[\p{Script=Han}a-z0-9]+(?:-[\p{Script=Han}a-z0-9]+)*$/u.test(value) &&
                    Array.from(value).length <= 64)
                    ? Promise.resolve()
                    : Promise.reject(
                        new Error(
                          '使用 1–64 个中文、小写字母、数字或连字符，连字符不能连续或位于首尾',
                        ),
                      ),
              },
            ]}
          >
            <Input placeholder="例如 会议纪要 或 meeting-notes" />
          </Form.Item>
          <Form.Item
            name="description"
            label="用途说明"
            rules={[{ required: true, whitespace: true, message: '请填写用途说明' }, { max: 2000 }]}
          >
            <Input.TextArea
              placeholder="这个 Skill 用来做什么，适合何时使用"
              autoSize={{ minRows: 3, maxRows: 6 }}
              maxLength={2000}
            />
          </Form.Item>
          {error && busy !== 'create' && <Alert type="error" showIcon message={error} />}
        </Form>
      </Modal>
      {importDialog && (
        <ImportDialog
          user={user}
          {...importDialog}
          onClose={() => setImportDialog(null)}
          onApplied={(message) => {
            setImportDialog(null);
            setNotice(message);
            void refresh();
          }}
        />
      )}
      {exports && (
        <ExportDialog
          user={user}
          ids={exports}
          onClose={() => setExports(null)}
          onDownloaded={() => {
            setExports(null);
            setSelected([]);
          }}
        />
      )}
      <DeleteConfirmation
        open={!!deleting}
        title="删除 Skill？"
        name={deleting?.name ?? ''}
        label="输入 Skill 名称确认"
        value={confirmation}
        onChange={setConfirmation}
        confirmText="永久删除"
        busy={!!busy}
        error={error}
        onCancel={() => setDeleting(null)}
        onConfirm={() => {
          if (deleting)
            void action('delete', async () => {
              await client.remove(user, deleting, confirmation);
              setDeleting(null);
              setSelected((current) => current.filter((id) => id !== deleting.id));
              setNotice('Skill 已删除');
              await refresh();
            });
        }}
      >
        <p>
          将删除此 Skill 及其资源文件和替换前版本，<strong>无法恢复</strong>。
        </p>
      </DeleteConfirmation>
    </section>
  );
}

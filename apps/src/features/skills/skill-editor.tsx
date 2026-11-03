import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Collapse, Drawer, Input, Modal, Select, Space, Spin, Tag } from 'antd';
import { Plus } from 'lucide-react';
import { useEffect, useState } from 'react';
import { APIError } from '../../api/error';
import { hasPermission, type CurrentUser } from '../../api/identity';
import { expireSession, useExpiredSession } from '../../shared/hooks/use-expired-session';
import { errorMessage } from '../../shared/utils/error-message';

import { client, date, size } from './skill-shared';

import { NewSkillFileDialog } from './new-skill-file-dialog';

export function SkillEditor({
  user,
  id,
  onClose,
  onSaved,
}: {
  user: CurrentUser;
  id: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const details = useQuery({
    queryKey: ['skills', user.id, 'detail', id],
    queryFn: ({ signal }) => client.get(user, id, signal),
    retry: false,
    refetchOnWindowFocus: false,
  });
  useExpiredSession(details.error);
  const [newFile, setNewFile] = useState(false);
  const [filename, setFilename] = useState('SKILL.md');
  const [loaded, setLoaded] = useState('');
  const [content, setContent] = useState('');
  const [original, setOriginal] = useState('');
  const [version, setVersion] = useState(0);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [reloading, setReloading] = useState(false);
  const [discard, setDiscard] = useState<{
    intent: 'close' | 'reload' | 'switch' | 'create';
    path?: string;
  } | null>(null);
  const cache = useQueryClient();
  const file = useQuery({
    queryKey: ['skills', user.id, 'file', id, filename],
    queryFn: ({ signal }) => client.file(user, id, filename, signal),
    enabled: !!details.data && filename !== 'SKILL.md',
    retry: false,
    refetchOnWindowFocus: false,
  });
  useExpiredSession(file.error);
  const selected =
    filename === 'SKILL.md'
      ? details.data
        ? { content: details.data.content ?? '', version: details.data.version, editable: true }
        : undefined
      : file.data;
  const fetching = filename === 'SKILL.md' ? details.isFetching : file.isFetching;
  const loadError = filename === 'SKILL.md' ? details.error : file.error;
  useEffect(() => {
    if (selected && !fetching && loaded !== filename) {
      setContent(selected.content);
      setOriginal(selected.content);
      setVersion(selected.version);
      setLoaded(filename);
    }
  }, [filename, selected?.content, selected?.version, fetching, loaded]);
  const dirty = loaded === filename && content !== original;
  function switchFile(path: string) {
    setFilename(path);
    setLoaded('');
    setError('');
  }
  function selectFile(path: string) {
    if (path === filename || saving || reloading) return;
    if (dirty) setDiscard({ intent: 'switch', path });
    else switchFile(path);
  }
  const close = () => {
    if (saving) return;
    if (dirty) setDiscard({ intent: 'close' });
    else onClose();
  };
  async function reload() {
    setReloading(true);
    try {
      const result = filename === 'SKILL.md' ? await details.refetch() : await file.refetch();
      if (result.data && !result.isError) {
        setContent(result.data.content ?? '');
        setOriginal(result.data.content ?? '');
        setVersion(result.data.version);
        setLoaded(filename);
        setError('');
      }
    } finally {
      setReloading(false);
    }
  }
  async function save() {
    if (saving || !details.data || !selected?.editable || loaded !== filename) return;
    setSaving(true);
    setError('');
    try {
      if (filename === 'SKILL.md') await client.save(user, { ...details.data, version }, content);
      else await client.saveFile(user, id, filename, version, content);
      onSaved();
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 401) expireSession(cache);
      setError(errorMessage(cause));
    } finally {
      setSaving(false);
    }
  }
  return (
    <>
      <Drawer
        open
        title={details.data?.name ?? 'Skill 详情'}
        width={840}
        onClose={close}
        maskClosable={!saving}
        closable={!saving}
        keyboard={!saving}
        className="skill-editor"
        extra={
          hasPermission(user, 'skills:manage') && (
            <Button
              type="primary"
              aria-label="保存"
              aria-busy={saving}
              disabled={!dirty || !!loadError || !selected?.editable || reloading}
              loading={saving}
              onClick={() => void save()}
            >
              保存
            </Button>
          )
        }
      >
        {details.isPending ? (
          <Spin aria-label="加载 Skill" />
        ) : details.isError ? (
          <Alert
            type="error"
            showIcon
            message={details.error.message}
            action={<Button onClick={() => void details.refetch()}>重试</Button>}
          />
        ) : (
          details.data && (
            <>
              <div className="skill-editor-summary">
                <p>{details.data.description}</p>
                <Space wrap>
                  <Tag>{details.data.file_count} 个文件</Tag>
                  <Tag>{size(details.data.size_bytes)}</Tag>
                  <span>更新于 {date(details.data.updated_at)}</span>
                </Space>
              </div>
              <div className="skill-file-picker">
                <label htmlFor="skill-file-picker">文件</label>
                <Select
                  id="skill-file-picker"
                  aria-label="文件"
                  value={filename}
                  virtual={false}
                  showSearch
                  optionFilterProp="label"
                  disabled={saving || reloading}
                  options={(details.data.files ?? [{ name: 'SKILL.md', size_bytes: 0 }]).map(
                    (item) => ({
                      value: item.name,
                      label: item.name,
                      title: `${item.name} · ${size(item.size_bytes)}`,
                    }),
                  )}
                  onChange={selectFile}
                />
                {hasPermission(user, 'skills:manage') && (
                  <Button
                    icon={<Plus size={16} aria-hidden="true" />}
                    disabled={saving || reloading}
                    onClick={() => {
                      if (dirty) setDiscard({ intent: 'create' });
                      else setNewFile(true);
                    }}
                  >
                    新建文件
                  </Button>
                )}
              </div>
              {error && (
                <Alert
                  type="error"
                  showIcon
                  message={error}
                  description="未保存的内容仍保留在编辑器中。版本冲突时，请先复制内容，再重新读取。"
                  action={
                    <Button
                      disabled={saving || reloading}
                      onClick={() => {
                        if (dirty) setDiscard({ intent: 'reload' });
                        else void reload();
                      }}
                    >
                      重新读取
                    </Button>
                  }
                />
              )}
              {loadError ? (
                <Alert
                  type="error"
                  showIcon
                  message={loadError.message}
                  action={
                    <Button loading={reloading} onClick={() => void reload()}>
                      重试
                    </Button>
                  }
                />
              ) : loaded !== filename ? (
                <Spin aria-label="加载文件" />
              ) : selected?.editable ? (
                <>
                  <label className="skill-content-label" htmlFor="skill-content">
                    {filename}
                  </label>
                  <Input.TextArea
                    id="skill-content"
                    className="skill-markdown"
                    value={content}
                    onChange={(event) => setContent(event.target.value)}
                    readOnly={!hasPermission(user, 'skills:manage')}
                    autoSize={{ minRows: 20, maxRows: 34 }}
                    spellCheck={false}
                  />
                  {filename === 'SKILL.md' && (
                    <p className="skill-note">名称须与当前 Skill 一致。</p>
                  )}
                </>
              ) : (
                <Alert type="info" showIcon message="此文件为二进制文件，请下载技能包后查看。" />
              )}
              {details.data.source_url && (
                <Collapse
                  ghost
                  items={[
                    {
                      key: 'source',
                      label: '来源与同步',
                      children: (
                        <div className="skill-source-detail">
                          <a
                            href={details.data.source_url}
                            target="_blank"
                            rel="noopener noreferrer"
                          >
                            {details.data.source_url}
                          </a>
                          <span>最近同步：{date(details.data.synced_at)}</span>
                        </div>
                      ),
                    },
                  ]}
                />
              )}
            </>
          )
        )}
      </Drawer>
      <Modal
        open={discard !== null}
        title="放弃未保存的修改？"
        okText="放弃修改"
        cancelText="继续编辑"
        onCancel={() => setDiscard(null)}
        onOk={() => {
          const pending = discard;
          setDiscard(null);
          if (pending?.intent === 'reload') void reload();
          else if (pending?.intent === 'switch' && pending.path) switchFile(pending.path);
          else if (pending?.intent === 'create') {
            setContent(original);
            setNewFile(true);
          } else onClose();
        }}
      >
        <p>尚未保存的内容会丢失。</p>
      </Modal>
      {newFile && details.data && (
        <NewSkillFileDialog
          user={user}
          skill={details.data}
          onClose={() => setNewFile(false)}
          onCreated={async (result, path) => {
            await cache.invalidateQueries({ queryKey: ['skills', user.id, 'file', id] });
            cache.setQueryData(['skills', user.id, 'detail', id], {
              ...details.data,
              ...result,
              content: details.data.content,
              files: [...(details.data.files ?? []), { name: path, size_bytes: 0 }],
            });
            setNewFile(false);
            switchFile(path);
            void cache.invalidateQueries({ queryKey: ['skills', user.id, 'list'] });
            void details.refetch();
          }}
        />
      )}
    </>
  );
}

import { useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Collapse, Input, Modal, Radio, Space, Tabs, Tag } from 'antd';
import { FileArchive, Link2, Upload } from 'lucide-react';
import { useRef, useState } from 'react';
import { APIError } from '../../api/error';
import { type CurrentUser } from '../../api/identity';
import { type SkillPreview } from '../../api/skills';
import { expireSession } from '../../shared/hooks/use-expired-session';
import { errorMessage } from '../../shared/utils/error-message';
import { SkillHubSearch } from './skill-hub-search';

import { client, size } from './skill-shared';

export function ImportDialog({
  user,
  initial,
  preview: initialPreview,
  onClose,
  onApplied,
}: {
  user: CurrentUser;
  initial: 'search' | 'file' | 'source';
  preview?: SkillPreview;
  onClose: () => void;
  onApplied: (message: string) => void;
}) {
  const [method, setMethod] = useState(initial);
  const [preview, setPreview] = useState(initialPreview);
  const [url, setUrl] = useState('');
  const [mode, setMode] = useState<'skip' | 'replace'>('skip');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const cache = useQueryClient();
  const fileInput = useRef<HTMLInputElement>(null);
  async function inspect(file?: File, source = url.trim()) {
    setBusy(true);
    setError('');
    try {
      setPreview(file ? await client.import(user, file) : await client.source(user, source));
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 401) expireSession(cache);
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }
  async function apply() {
    if (!preview?.token) return;
    setBusy(true);
    setError('');
    try {
      const result = await client.apply(user, preview, mode);
      onApplied(
        `已保存 ${result.applied.length} 项${result.skipped.length ? `，保留现有 ${result.skipped.length} 项` : ''}`,
      );
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 401) expireSession(cache);
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }
  const conflicts = preview?.items.some((item) => item.conflict);
  return (
    <Modal
      open
      title={preview ? '确认导入 Skill' : '导入技能包'}
      width={640}
      onCancel={onClose}
      maskClosable={!busy}
      closable={!busy}
      keyboard={!busy}
      footer={
        !preview && method === 'search' ? (
          <Button disabled={busy} onClick={onClose}>
            取消
          </Button>
        ) : undefined
      }
      cancelButtonProps={{ disabled: busy }}
      okText={preview ? '确认导入' : method === 'source' ? '获取并预览' : '选择文件'}
      confirmLoading={busy}
      okButtonProps={{
        disabled: preview
          ? preview.unchanged || !preview.token
          : method === 'source' && !url.trim(),
      }}
      onOk={() => {
        if (preview) void apply();
        else if (method === 'source') void inspect();
        else fileInput.current?.click();
      }}
    >
      {error && <Alert type="error" showIcon message={error} />}
      {!preview ? (
        <>
          <Tabs
            activeKey={method}
            onChange={(key) => {
              setMethod(key as 'search' | 'file' | 'source');
              setError('');
            }}
            items={[
              {
                key: 'search',
                label: '搜索技能',
                disabled: busy,
                children: (
                  <SkillHubSearch
                    user={user}
                    busy={busy}
                    onPreview={(source) => {
                      setUrl(source);
                      void inspect(undefined, source);
                    }}
                  />
                ),
              },
              {
                key: 'source',
                label: '链接导入',
                disabled: busy,
                children: (
                  <div className="skill-import-form">
                    <label htmlFor="skill-source-url">公开链接</label>
                    <Input
                      id="skill-source-url"
                      placeholder="https://github.com/owner/repo"
                      value={url}
                      disabled={busy}
                      onChange={(e) => setUrl(e.target.value)}
                      onPressEnter={() => {
                        if (url.trim() && !busy) void inspect();
                      }}
                    />
                    <p className="skill-note">
                      支持 GitHub 仓库、技能目录和文件链接，也支持 Markdown、ZIP
                      直链。先预览，再导入。
                    </p>
                  </div>
                ),
              },
              {
                key: 'file',
                label: '文件导入',
                disabled: busy,
                children: (
                  <div className="skill-import-form">
                    <div className="skill-upload-target">
                      <FileArchive size={34} aria-hidden="true" />
                      <p>Markdown 文件或 ZIP 技能包</p>
                      <Button
                        icon={<Upload size={16} aria-hidden="true" />}
                        loading={busy}
                        onClick={() => fileInput.current?.click()}
                      >
                        选择文件
                      </Button>
                      <span>保留包内资源目录</span>
                    </div>
                  </div>
                ),
              },
            ]}
          />
          <input
            ref={fileInput}
            type="file"
            accept=".md,.zip"
            hidden
            disabled={busy}
            aria-label="选择 Skill 文件"
            onChange={(event) => {
              const file = event.target.files?.[0];
              if (file) void inspect(file);
              event.target.value = '';
            }}
          />
        </>
      ) : (
        <div className="skill-import-preview">
          {preview.source_url && (
            <p className="skill-source-url" title={preview.source_url}>
              <Link2 size={14} aria-hidden="true" />
              {preview.source_url}
            </p>
          )}
          {preview.unchanged && <Alert type="success" showIcon message="已是最新内容" />}
          {preview.items.map((item) => (
            <article className="skill-preview-item" key={item.name}>
              <div>
                <strong>{item.name}</strong>
                <Tag color={item.conflict ? 'orange' : 'blue'}>
                  {item.conflict ? '已存在' : '新增'}
                </Tag>
              </div>
              <p>{item.description}</p>
              {item.local_changed && (
                <Alert
                  type="warning"
                  showIcon
                  message="此 Skill 有本地修改，替换前会保留上一版。"
                />
              )}
              <Collapse
                ghost
                items={[
                  {
                    key: 'files',
                    label: `${item.files.length} 个文件 · ${item.changed_files.length} 个差异`,
                    children: (
                      <ul className="skill-file-list">
                        {item.files.map((file) => (
                          <li key={file.name}>
                            <code>{file.name}</code>
                            <span>{size(file.size_bytes)}</span>
                          </li>
                        ))}
                        {item.changed_files
                          .filter((name) => !item.files.some((file) => file.name === name))
                          .map((name) => (
                            <li key={name}>
                              <code>{name}</code>
                              <span>将移除</span>
                            </li>
                          ))}
                      </ul>
                    ),
                  },
                ]}
              />
            </article>
          ))}
          {conflicts && !preview.unchanged && (
            <fieldset className="skill-conflict-options">
              <legend>同名 Skill</legend>
              <Radio.Group value={mode} onChange={(event) => setMode(event.target.value)}>
                <Space direction="vertical">
                  <Radio value="skip">保留现有，跳过同名项</Radio>
                  <Radio value="replace">替换同名项，并保留上一版供下载</Radio>
                </Space>
              </Radio.Group>
            </fieldset>
          )}
        </div>
      )}
    </Modal>
  );
}

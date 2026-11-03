import { useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Input, Modal } from 'antd';
import { useState } from 'react';
import { APIError } from '../../api/error';
import { type CurrentUser } from '../../api/identity';
import { type Skill } from '../../api/skills';
import { expireSession } from '../../shared/hooks/use-expired-session';
import { errorMessage } from '../../shared/utils/error-message';

import { client } from './skill-shared';

export function NewSkillFileDialog({
  user,
  skill,
  onClose,
  onCreated,
}: {
  user: CurrentUser;
  skill: Skill;
  onClose: () => void;
  onCreated: (skill: Skill, path: string) => Promise<void>;
}) {
  const [path, setPath] = useState('');
  const [content, setContent] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [discard, setDiscard] = useState(false);
  const [conflict, setConflict] = useState(false);
  const cache = useQueryClient();
  function close() {
    if (busy) return;
    if (path || content) setDiscard(true);
    else onClose();
  }
  async function create() {
    if (busy || !path.trim()) return;
    setBusy(true);
    setError('');
    setConflict(false);
    try {
      const result = await client.createFile(user, skill.id, path.trim(), skill.version, content);
      await onCreated(result, path.trim());
    } catch (cause) {
      setConflict(cause instanceof APIError && cause.status === 409);
      if (cause instanceof APIError && cause.status === 401) expireSession(cache);
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }
  return (
    <>
      <Modal
        open
        title="新建文件"
        width={720}
        okText="创建文件"
        cancelText="取消"
        onCancel={close}
        onOk={() => void create()}
        confirmLoading={busy}
        okButtonProps={{ disabled: !path.trim(), 'aria-label': '创建文件', 'aria-busy': busy }}
        cancelButtonProps={{ disabled: busy }}
        closable={!busy}
        maskClosable={!busy}
        keyboard={!busy}
      >
        <label className="skill-content-label" htmlFor="skill-new-file-path">
          文件路径
        </label>
        <Input
          id="skill-new-file-path"
          placeholder="例如 scripts/run.py"
          maxLength={512}
          value={path}
          disabled={busy}
          onChange={(event) => setPath(event.target.value)}
        />
        <p className="skill-note">
          填写技能包内的相对路径，支持 scripts/run.py、scripts/run.sh、references/guide.md 等文件。
        </p>
        <label className="skill-content-label" htmlFor="skill-new-file-content">
          文件内容
        </label>
        <Input.TextArea
          id="skill-new-file-content"
          className="skill-markdown"
          value={content}
          disabled={busy}
          onChange={(event) => setContent(event.target.value)}
          autoSize={{ minRows: 12, maxRows: 24 }}
          spellCheck={false}
        />
        {error && (
          <Alert
            type="error"
            showIcon
            message={error}
            action={
              conflict ? (
                <Button
                  disabled={busy}
                  onClick={() =>
                    void cache.invalidateQueries({
                      queryKey: ['skills', user.id, 'detail', skill.id],
                    })
                  }
                >
                  刷新版本
                </Button>
              ) : undefined
            }
          />
        )}
      </Modal>
      <Modal
        open={discard}
        title="放弃新建文件？"
        okText="放弃"
        cancelText="继续编辑"
        onCancel={() => setDiscard(false)}
        onOk={onClose}
      >
        <p>尚未创建的文件内容会丢失。</p>
      </Modal>
    </>
  );
}

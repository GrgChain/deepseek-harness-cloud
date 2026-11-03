import { useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Collapse, Modal, Spin } from 'antd';
import { useState } from 'react';
import { APIError } from '../../api/error';
import { type CurrentUser } from '../../api/identity';
import { expireSession, useExpiredSession } from '../../shared/hooks/use-expired-session';
import { errorMessage } from '../../shared/utils/error-message';

import { client, download, size } from './skill-shared';

export function ExportDialog({
  user,
  ids,
  onClose,
  onDownloaded,
}: {
  user: CurrentUser;
  ids: string[];
  onClose: () => void;
  onDownloaded: () => void;
}) {
  const details = useQuery({
    queryKey: ['skills', user.id, 'export', ids],
    queryFn: () => Promise.all(ids.map((id) => client.get(user, id))),
    retry: false,
  });
  useExpiredSession(details.error);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const cache = useQueryClient();
  async function run() {
    setBusy(true);
    setError('');
    try {
      const file = await client.download(user, ids);
      download(file.blob, file.name);
      onDownloaded();
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 401) expireSession(cache);
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      open
      title={`导出 ${ids.length} 项 Skill`}
      okText="下载"
      cancelText="取消"
      onCancel={onClose}
      maskClosable={!busy}
      closable={!busy}
      keyboard={!busy}
      cancelButtonProps={{ disabled: busy }}
      okButtonProps={{ disabled: !details.data }}
      confirmLoading={busy}
      onOk={() => void run()}
    >
      <p>
        单项无附属文件时下载 Markdown，其他情况打包为 ZIP。请确认正文和资源中没有不宜分享的内容。
      </p>
      {(error || details.isError) && (
        <Alert type="error" showIcon message={error || details.error?.message} />
      )}
      {details.isPending ? (
        <Spin aria-label="检查导出文件" />
      ) : (
        <Collapse
          items={details.data?.map((skill) => ({
            key: skill.id,
            label: `${skill.name} · ${skill.file_count} 个文件`,
            children: (
              <ul className="skill-file-list">
                {skill.files?.map((file) => (
                  <li key={file.name}>
                    <code>{file.name}</code>
                    <span>{size(file.size_bytes)}</span>
                  </li>
                ))}
              </ul>
            ),
          }))}
        />
      )}
    </Modal>
  );
}

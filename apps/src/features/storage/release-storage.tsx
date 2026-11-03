import { useQueryClient } from '@tanstack/react-query';
import { Alert, Button } from 'antd';
import { useState } from 'react';
import { APIError } from '../../api/error';
import type { CurrentUser } from '../../api/identity';
import { type StorageAsset } from '../../api/storage';
import { DeleteConfirmation } from '../../shared/components/delete-confirmation';
import { expireSession } from '../../shared/hooks/use-expired-session';

import { blockers, bytes, client } from './storage-shared';

export function ReleaseStorage({ asset, user }: { asset: StorageAsset; user: CurrentUser }) {
  const cache = useQueryClient();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [submitted, setSubmitted] = useState(false);
  const [error, setError] = useState('');
  const allowed = user.permissions.includes('instances:release-storage');
  const blocked = asset.maintenance.reclaim_blockers.filter(
    (code) => !['RETENTION_ACTIVE', 'MINIMUM_7_DAYS'].includes(code),
  );
  const pending = asset.status === 'RECLAIM_PENDING' || (submitted && asset.status !== 'DELETED');
  async function release() {
    setBusy(true);
    setError('');
    try {
      await client.release(user, asset.id, name);
      setSubmitted(true);
      setOpen(false);
      void cache.invalidateQueries({ queryKey: ['storage'] });
      void cache.invalidateQueries({ queryKey: ['preflight'] });
    } catch (e) {
      if (e instanceof APIError && e.status === 401) expireSession(cache);
      setError(e instanceof Error ? e.message : '释放结果待确认，请刷新查看');
    } finally {
      setBusy(false);
    }
  }
  if (asset.status === 'DELETED') return <p className="storage-muted">存储已释放</p>;
  if (pending) return <Alert type="info" showIcon message="释放中，完成后返还存储额度" />;
  if (!allowed || asset.workspace_state !== 'DELETED') return null;
  return (
    <>
      <Button
        danger
        disabled={blocked.length > 0}
        onClick={() => {
          setName('');
          setError('');
          setOpen(true);
        }}
      >
        释放存储
      </Button>
      {blocked.length > 0 && (
        <p className="storage-muted">
          {blocked.map((code) => blockers[code] ?? '条件待核对').join('；')}
        </p>
      )}
      <DeleteConfirmation
        title="释放存储"
        open={open}
        name={asset.workspace_name}
        label="确认工作空间名称"
        value={name}
        onChange={setName}
        busy={busy}
        error={error}
        confirmText="永久释放"
        onCancel={() => setOpen(false)}
        onConfirm={() => void release()}
      >
        <p>
          <strong>工作数据将永久删除，无法恢复。</strong>
        </p>
        <p>回收完成后释放 {bytes(asset.capacity_bytes)} 存储额度。</p>
      </DeleteConfirmation>
    </>
  );
}

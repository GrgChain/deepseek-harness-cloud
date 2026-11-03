import { useQuery } from '@tanstack/react-query';
import { Alert, Button, Spin } from 'antd';
import { ArrowLeft } from 'lucide-react';
import { useNavigate, useParams } from 'react-router-dom';
import type { CurrentUser } from '../../api/identity';
import { AssetStatus } from '../../shared/components/resource-status';
import { useExpiredSession } from '../../shared/hooks/use-expired-session';

import { bytes, client } from './storage-shared';

import { Usage } from './storage-summary';

import { FileBrowser } from './storage-file-browser';

export function StorageBrowsePage({ user }: { user: CurrentUser }) {
  const { assetId = '' } = useParams();
  const navigate = useNavigate();
  const details = useQuery({
    queryKey: ['storage', user.id, 'asset', assetId],
    queryFn: ({ signal }) => client.details(user, assetId, signal),
    retry: false,
  });
  useExpiredSession(details.error);
  if (details.isPending)
    return (
      <div className="storage-browser-loading">
        <Spin aria-label="加载数据资产" />
      </div>
    );
  if (details.isError)
    return (
      <Alert
        type="error"
        showIcon
        message="无法打开数据资产"
        description={details.error.message}
        action={<Button onClick={() => navigate('/app/storage')}>返回工作数据</Button>}
      />
    );
  const asset = details.data;
  return (
    <section className="storage-browser-page" aria-label="数据浏览">
      <header className="storage-browser-heading">
        <Button
          className="storage-browser-back"
          type="text"
          icon={<ArrowLeft size={16} aria-hidden="true" />}
          onClick={() => navigate('/app/storage')}
        >
          返回工作数据
        </Button>
        <div className="storage-browser-identity">
          <div className="storage-browser-title">
            <h2>{asset.workspace_name}</h2>
            <AssetStatus status={asset.status} />
          </div>
          <div className="storage-browser-meta">
            <span className="storage-browser-capacity">
              <small>已分配</small>
              <strong>{bytes(asset.capacity_bytes)}</strong>
            </span>
            <span className="storage-browser-meta-divider" aria-hidden="true" />
            <span className="storage-browser-usage">
              <small>数据用量</small>
              <Usage asset={asset} />
            </span>
          </div>
        </div>
      </header>
      <FileBrowser user={user} asset={asset} />
    </section>
  );
}

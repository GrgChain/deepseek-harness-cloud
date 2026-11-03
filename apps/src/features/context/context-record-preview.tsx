import { Button, Modal, Tag } from 'antd';

import { KnowledgeContent } from './knowledge-content';

import { date, ManagedRecord, memoryCategory } from './context-shared';

interface ContextRecordPreviewProps {
  detail: ManagedRecord | null;
  nativeMemory: boolean;
  onClose: () => void;
}

export function ContextRecordPreview({ detail, nativeMemory, onClose }: ContextRecordPreviewProps) {
  return (
    <Modal
      className="context-original-modal"
      width={800}
      open={!!detail}
      title={detail?.title}
      footer={<Button onClick={onClose}>关闭</Button>}
      onCancel={onClose}
    >
      <div className="context-original-meta">
        {detail?.memory_source === 'automatic' ? (
          <>
            <Tag color="blue">自动生成</Tag>
            <span>
              <span className="context-original-meta-label">分类</span>
              {memoryCategory(detail.category)}
            </span>
          </>
        ) : (
          <>
            <Tag>v{detail?.version}</Tag>
            {nativeMemory && <Tag>手动添加</Tag>}
            {detail?.source && (
              <span>
                <span className="context-original-meta-label">来源</span>
                {detail.source}
              </span>
            )}
          </>
        )}
        {detail?.updated_at && !Number.isNaN(Date.parse(detail.updated_at)) && (
          <span>
            <span className="context-original-meta-label">更新于</span>
            {date(detail.updated_at)}
          </span>
        )}
      </div>
      <KnowledgeContent text={detail?.content ?? ''} className="context-content" label="原文内容" />
    </Modal>
  );
}

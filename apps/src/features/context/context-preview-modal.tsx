import { Alert, Checkbox, Empty, Modal, Radio, Tag } from 'antd';
import type { ReactNode } from 'react';
import type { ContextPreview } from '../../api/context';
import { DataPagination, usePagePagination } from '../../shared/components/data-pagination';
import { memorySource } from './context-shared';
import { KnowledgeContent } from './knowledge-content';

export interface ContextPreviewModalProps {
  preview: ContextPreview | null;
  previewImport: boolean;
  memory: boolean;
  nativeMemory: boolean;
  busy: string;
  operationPending: boolean;
  error: string;
  pendingFeedback: ReactNode;
  mode: string;
  acceptLoss: boolean;
  onModeChange: (value: string) => void;
  onAcceptLossChange: (checked: boolean) => void;
  onCancel: () => void;
  onConfirm: () => void;
}

export function ContextPreviewModal({
  preview,
  previewImport,
  memory,
  nativeMemory,
  busy,
  operationPending,
  error,
  pendingFeedback,
  mode,
  acceptLoss,
  onModeChange,
  onAcceptLossChange,
  onCancel,
  onConfirm,
}: ContextPreviewModalProps) {
  const previewPagination = usePagePagination(preview?.token ?? '');
  const previewItems = previewImport
    ? preview?.items.slice(
        (previewPagination.page - 1) * previewPagination.pageSize,
        previewPagination.page * previewPagination.pageSize,
      )
    : preview?.items;

  return (
    <Modal
      className="context-preview-modal"
      width={640}
      open={!!preview}
      title={previewImport ? '确认导入记忆' : memory ? '确认发送到外部服务' : '确认保存文档'}
      okText={
        busy ? '正在保存' : operationPending ? '继续核实' : previewImport ? '确认导入' : '确认保存'
      }
      cancelText="取消"
      confirmLoading={busy === 'apply' || busy === 'reconcile'}
      okButtonProps={{
        disabled:
          !!busy ||
          (previewImport && !preview?.items.length) ||
          (!!preview?.warnings.length && !acceptLoss),
      }}
      cancelButtonProps={{ disabled: !!busy }}
      closable={!busy}
      maskClosable={!busy}
      keyboard={!busy}
      onCancel={onCancel}
      onOk={onConfirm}
    >
      {pendingFeedback}
      {error && !operationPending && <Alert type="error" message={error} />}
      <Alert type="info" showIcon message={preview?.data_use} />
      {previewImport && (
        <p className="context-import-summary">
          {preview?.source_counts
            ? `自动生成 ${preview.source_counts.automatic} 条 · 手动添加 ${preview.source_counts.manual} 条`
            : `共 ${preview?.items.length ?? 0} 条记忆`}{' '}
          · 确认后导入全部内容
        </p>
      )}
      {previewImport && !preview?.items.length && (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="没有可导入的记忆" />
      )}
      {previewItems?.map((item) => (
        <article className="context-preview" key={`${item.memory_source ?? 'manual'}:${item.id}`}>
          <div className="context-preview-heading">
            <div className="context-preview-identity">
              <strong>{item.title}</strong>
              {nativeMemory && previewImport && (
                <Tag color={item.memory_source === 'automatic' ? 'blue' : undefined}>
                  {memorySource(item)}
                </Tag>
              )}
            </div>
            <Tag color={item.conflict ? 'orange' : 'blue'}>{item.conflict ? '已存在' : '新增'}</Tag>
          </div>
          {memory ? (
            <p>{item.content?.slice(0, 1200)}</p>
          ) : (
            <KnowledgeContent
              text={item.content?.slice(0, 1200) ?? ''}
              className="context-preview-content"
              label="原文内容"
            />
          )}
          <small>来源：{item.source}</small>
        </article>
      ))}
      {previewImport && !!preview?.items.length && (
        <DataPagination
          label="导入预览"
          pagination={previewPagination}
          count={previewItems?.length ?? 0}
          total={preview.items.length}
          loading={!!busy}
          failed={false}
        />
      )}
      {preview?.items.some((v) => v.conflict) && (
        <Radio.Group
          disabled={!!busy || operationPending}
          value={mode}
          onChange={(e) => onModeChange(e.target.value)}
        >
          <Radio value="skip">保留现有，跳过冲突</Radio>
          <Radio value="replace">替换现有内容</Radio>
        </Radio.Group>
      )}
      {!!preview?.warnings.length && (
        <>
          <Alert
            type="warning"
            showIcon
            message="迁移有语义损失"
            description={preview.warnings.join(' ')}
          />
          <Checkbox
            disabled={!!busy || operationPending}
            checked={acceptLoss}
            onChange={(e) => onAcceptLossChange(e.target.checked)}
          >
            已了解并接受上述语义损失
          </Checkbox>
        </>
      )}
    </Modal>
  );
}

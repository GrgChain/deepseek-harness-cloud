import { Alert, Checkbox, Modal, Spin } from 'antd';

import { FileText } from 'lucide-react';

import { type ContextRecord } from '../../api/context';

interface ContextDeleteDialogProps {
  memory: boolean;
  removing: ContextRecord | null;
  busy: string;
  operationPending: boolean;
  confirmed: boolean;
  error: string;
  onConfirmedChange: (confirmed: boolean) => void;
  onCancel: () => void;
  onConfirm: () => void;
}

export function ContextDeleteDialog({
  memory,
  removing,
  busy,
  operationPending,
  confirmed,
  error,
  onConfirmedChange,
  onCancel,
  onConfirm,
}: ContextDeleteDialogProps) {
  return (
    <Modal
      className="context-delete-modal"
      width={520}
      open={!!removing}
      title={memory ? '永久删除此记忆？' : '删除文档'}
      okText={busy === 'delete' ? '正在删除' : '永久删除'}
      cancelText="取消"
      okButtonProps={{ danger: true, disabled: !confirmed || !!busy }}
      cancelButtonProps={{ disabled: !!busy }}
      closable={!busy}
      maskClosable={!busy}
      keyboard={!busy}
      confirmLoading={busy === 'delete'}
      onCancel={onCancel}
      onOk={onConfirm}
    >
      {memory ? (
        <p>删除「{removing?.title}」的外部内容及索引。操作不可恢复。</p>
      ) : (
        <>
          <div className="context-delete-target">
            <FileText size={20} aria-hidden="true" />
            <div>
              <span>文档名称</span>
              <strong>{removing?.title}</strong>
            </div>
          </div>
          <Alert type="warning" showIcon message="删除后无法恢复，相关索引将一并移除。" />
        </>
      )}
      <Checkbox
        checked={confirmed}
        disabled={!!busy}
        onChange={(e) => onConfirmedChange(e.target.checked)}
      >
        我确认永久删除
      </Checkbox>
      {busy === 'delete' && operationPending && (
        <Alert
          role="status"
          aria-live="polite"
          type="info"
          showIcon
          icon={<Spin size="small" />}
          message="正在删除"
          description="正在删除外部内容及索引，完成后将自动更新列表。"
        />
      )}
      {error && <Alert type="error" showIcon message={error} />}
    </Modal>
  );
}

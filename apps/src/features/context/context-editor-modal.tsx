import { Alert, Button, Form, Input, Modal, Segmented } from 'antd';
import { FileText, Upload } from 'lucide-react';
import { useRef, type ReactNode } from 'react';
import type { ContextRecord } from '../../api/context';
import { documentAccept } from './document-import';
import { ContentViewSwitcher, MarkdownContent, type ContentView } from './knowledge-content';

export interface ContextEditorModalProps {
  open: boolean;
  memory: boolean;
  target: ContextRecord | null;
  busy: string;
  operationPending: boolean;
  error: string;
  pendingFeedback: ReactNode;
  documentMode: 'text' | 'file';
  documentName: string;
  editorView: ContentView;
  title: string;
  content: string;
  source: string;
  onCancel: () => void;
  onSubmit: () => void;
  onDocumentModeChange: (mode: 'text' | 'file') => void;
  onSelectDocument: (file: File) => void;
  onTitleChange: (value: string) => void;
  onContentChange: (value: string) => void;
  onSourceChange: (value: string) => void;
  onEditorViewChange: (view: ContentView) => void;
}

export function ContextEditorModal({
  open,
  memory,
  target,
  busy,
  operationPending,
  error,
  pendingFeedback,
  documentMode,
  documentName,
  editorView,
  title,
  content,
  source,
  onCancel,
  onSubmit,
  onDocumentModeChange,
  onSelectDocument,
  onTitleChange,
  onContentChange,
  onSourceChange,
  onEditorViewChange,
}: ContextEditorModalProps) {
  const documentInput = useRef<HTMLInputElement>(null);

  return (
    <Modal
      className="context-content-editor"
      width={640}
      open={open}
      title={target ? '编辑内容' : memory ? '添加记忆' : '添加文档'}
      okText="预览"
      cancelText="取消"
      confirmLoading={busy === 'preview'}
      cancelButtonProps={{ disabled: !!busy }}
      closable={!busy}
      maskClosable={!busy}
      keyboard={!busy}
      onCancel={onCancel}
      okButtonProps={{
        disabled:
          !!busy ||
          operationPending ||
          !title.trim() ||
          !content.trim() ||
          (!memory && !target && documentMode === 'file' && !documentName),
      }}
      onOk={onSubmit}
    >
      {pendingFeedback}
      {error && !operationPending && <Alert type="error" showIcon message={error} />}
      <Form layout="vertical">
        {!memory && !target && (
          <>
            <Form.Item className="context-document-mode" label="添加方式">
              <Segmented
                block
                aria-label="添加方式"
                value={documentMode}
                disabled={!!busy}
                options={[
                  { label: '输入文字', value: 'text' },
                  { label: '选择文件', value: 'file' },
                ]}
                onChange={(value) => onDocumentModeChange(value as 'text' | 'file')}
              />
            </Form.Item>
            {documentMode === 'file' && (
              <div className="context-document-upload">
                <div className="context-document-upload-main">
                  <span className="context-document-upload-icon">
                    <FileText size={20} aria-hidden="true" />
                  </span>
                  <div className="context-document-upload-copy" aria-live="polite">
                    <strong>{documentName || '选择文档文件'}</strong>
                    <span>
                      {busy === 'document'
                        ? '正在解析文件...'
                        : documentName
                          ? '解析完成，可检查和编辑内容'
                          : 'Word、Excel、PowerPoint、PDF、图片及文本'}
                    </span>
                  </div>
                  <Button
                    icon={<Upload size={15} aria-hidden="true" />}
                    loading={busy === 'document'}
                    disabled={!!busy}
                    onClick={() => documentInput.current?.click()}
                  >
                    {documentName ? '更换文件' : '选择文件'}
                  </Button>
                </div>
                <input
                  ref={documentInput}
                  className="context-document-file-input"
                  aria-label="选择文档文件"
                  type="file"
                  accept={documentAccept}
                  onChange={(event) => {
                    const file = event.target.files?.[0];
                    event.target.value = '';
                    if (file) onSelectDocument(file);
                  }}
                />
                <p className="context-document-upload-note">
                  文件最大 16 MiB，提取文字最多 256 KiB。文件发送至平台解析，仅保存提取的文字。
                </p>
              </div>
            )}
          </>
        )}
        {(memory || !!target || documentMode === 'text' || !!documentName) && (
          <>
            <Form.Item label="标题" htmlFor="context-content-title" required>
              <Input
                id="context-content-title"
                placeholder={memory ? '例如：回答偏好、项目约定' : '文档标题'}
                value={title}
                maxLength={200}
                disabled={!!busy}
                onChange={(e) => onTitleChange(e.target.value)}
              />
            </Form.Item>
            <Form.Item
              label="内容"
              htmlFor={memory || editorView === 'source' ? 'context-content-body' : undefined}
              required
            >
              {!memory && <ContentViewSwitcher value={editorView} onChange={onEditorViewChange} />}
              {memory || editorView === 'source' ? (
                <Input.TextArea
                  id="context-content-body"
                  placeholder={memory ? '写下希望保留的偏好、经验或事实' : '输入需要保存的文档内容'}
                  autoSize={{ minRows: 6, maxRows: 12 }}
                  value={content}
                  maxLength={262144}
                  disabled={!!busy}
                  onChange={(e) => onContentChange(e.target.value)}
                />
              ) : (
                <div
                  className="context-editor-markdown"
                  role="region"
                  aria-label="Markdown 预览内容"
                  tabIndex={0}
                >
                  <MarkdownContent text={content} />
                </div>
              )}
            </Form.Item>
            <Form.Item label="来源" htmlFor="context-content-source">
              <Input
                id="context-content-source"
                placeholder="选填，例如会议纪要或本人确认"
                value={source}
                maxLength={1000}
                disabled={!!busy}
                onChange={(e) => onSourceChange(e.target.value)}
              />
            </Form.Item>
          </>
        )}
      </Form>
    </Modal>
  );
}

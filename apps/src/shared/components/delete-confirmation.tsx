import { useId, useRef, useState, type ReactNode } from 'react';
import { Alert, Input, Modal, theme } from 'antd';
import { TriangleAlert } from 'lucide-react';
import './delete-confirmation.css';

type DeleteConfirmationProps = {
  open: boolean;
  title: string;
  name: string;
  label: string;
  value: string;
  onChange: (value: string) => void;
  onConfirm: () => void;
  onCancel: () => void;
  children: ReactNode;
  confirmText?: string;
  busy?: boolean;
  error?: string;
};

export function DeleteConfirmation({
  open,
  title,
  name,
  label,
  value,
  onChange,
  onConfirm,
  onCancel,
  children,
  confirmText = '确认删除',
  busy = false,
  error,
}: DeleteConfirmationProps) {
  const id = useId();
  const input = useRef<import('antd').InputRef>(null);
  const [blurred, setBlurred] = useState(false);
  const { token } = theme.useToken();
  const matched = !!name && value === name;
  const mismatch = !!value && !matched && blurred;
  return (
    <Modal
      className="delete-confirmation"
      width={480}
      centered
      open={open}
      title={
        <span className="delete-confirmation-title">
          <TriangleAlert size={20} color={token.colorError} aria-hidden="true" />
          {title}
        </span>
      }
      afterOpenChange={(visible) => {
        if (visible) {
          setBlurred(false);
          input.current?.focus();
        }
      }}
      onCancel={() => {
        if (!busy) onCancel();
      }}
      closable={!busy}
      maskClosable={false}
      keyboard={!busy}
      cancelText="取消"
      cancelButtonProps={{ disabled: busy }}
      okText={busy ? '正在提交…' : confirmText}
      confirmLoading={busy}
      okButtonProps={{ danger: true, disabled: busy || !matched }}
      onOk={() => {
        if (!busy && matched) onConfirm();
      }}
      destroyOnHidden
    >
      <div className="delete-confirmation-target">
        <span>操作对象</span>
        <strong>{name}</strong>
      </div>
      <div className="delete-confirmation-impact">{children}</div>
      <div className="delete-confirmation-field">
        <label htmlFor={id}>{label}</label>
        <p id={`${id}-hint`}>请输入上方完整名称以确认操作。</p>
        <Input
          ref={input}
          id={id}
          aria-label={label}
          aria-describedby={`${id}-hint ${id}-feedback`}
          aria-invalid={mismatch}
          autoComplete="off"
          spellCheck={false}
          placeholder="输入完整名称"
          value={value}
          disabled={busy}
          status={mismatch ? 'error' : undefined}
          onBlur={() => setBlurred(true)}
          onChange={(event) => {
            setBlurred(false);
            onChange(event.target.value);
          }}
        />
        <div
          id={`${id}-feedback`}
          className="delete-confirmation-feedback"
          role="status"
          style={{
            color: matched
              ? token.colorSuccess
              : mismatch
                ? token.colorError
                : token.colorTextSecondary,
          }}
        >
          {matched
            ? '名称已匹配'
            : mismatch
              ? '名称不一致，请检查后重新输入。'
              : '名称需完全一致，区分大小写。'}
        </div>
      </div>
      {error && <Alert role="alert" type="error" showIcon message={error} />}
    </Modal>
  );
}

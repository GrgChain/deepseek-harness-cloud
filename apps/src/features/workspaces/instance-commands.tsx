import { useMutation, useQuery } from '@tanstack/react-query';
import { Alert, Button, Form, Modal, Select, Space } from 'antd';
import { ArrowRight } from 'lucide-react';
import { useState } from 'react';
import { type Acceptance, type CommandAction } from '../../api/commands';
import { APIError } from '../../api/error';
import { hasPermission, type CurrentUser } from '../../api/identity';
import { isPersonalModelUsable } from '../../api/personal-models';
import type { Instance, Operation } from '../../api/resources';
import { DeleteConfirmation } from '../../shared/components/delete-confirmation';
import { ProviderLabel, ProviderLogo } from '../../shared/components/provider-logo';
import './instance-model-update.css';

import { commands, pending, providerAccounts, useCommandResult } from './instance-command-state';

export function RetryOperationButton({
  user,
  instance,
  operation,
}: {
  user: CurrentUser;
  instance: Instance;
  operation: Operation | undefined;
}) {
  const result = useCommandResult(user);
  const unresolved = pending(user, 'retry', instance.id);
  const retry = useMutation({
    mutationFn: () =>
      unresolved
        ? commands.confirmPending(user, 'retry', instance.id)
        : commands.retry(user, instance.id, operation!.id),
    ...result,
  });
  if (!hasPermission(user, 'instances:retry') || (!unresolved && !operation?.retry_allowed))
    return null;
  return (
    <Space direction="vertical" style={{ marginBottom: 16 }}>
      <Button loading={retry.isPending} onClick={() => retry.mutate()}>
        {unresolved ? '确认上次重试结果' : '重试失败操作'}
      </Button>
      {retry.isError && <Alert type="error" showIcon message={retry.error.message} />}
    </Space>
  );
}

export function InstanceCommands({ user, instance }: { user: CurrentUser; instance: Instance }) {
  const [selectedProvider, setSelectedProvider] = useState<string>();
  const result = useCommandResult(user);
  const [dialog, setDialog] = useState<'delete' | 'configure' | null>(null);
  const [name, setName] = useState('');
  const [version, setVersion] = useState(instance.version);
  const canReadModels = hasPermission(user, 'personal-model:read');
  const accounts = useQuery({
    queryKey: ['provider-accounts', user.id],
    enabled: dialog === 'configure' && canReadModels,
    queryFn: ({ signal }) => providerAccounts.providerAccounts(user, signal),
    refetchInterval: 5000,
    retry: false,
  });
  const providers = useQuery({
    queryKey: ['model-providers', user.id],
    enabled: dialog === 'configure' && canReadModels,
    queryFn: ({ signal }) => providerAccounts.providers(user, signal),
    retry: false,
  });
  const activeAccounts = (accounts.data?.items ?? []).filter(
    (account) => account.status === 'ACTIVE',
  );
  const account = activeAccounts.find((item) => item.id === selectedProvider);
  const choice = account?.models.find((item) => item.id === account.default_model_id);
  const sameModel =
    !!choice &&
    choice.id === instance.config_snapshot.model_configuration_id &&
    choice.version === instance.config_snapshot.model_configuration_version;
  const usable = canReadModels && !accounts.isError && isPersonalModelUsable(choice);
  const providerName = (id: string) =>
    id.startsWith('custom-')
      ? '自定义'
      : (providers.data?.items.find((provider) => provider.id === id)?.name ?? id);
  const providerOptions = activeAccounts
    .filter((account) =>
      isPersonalModelUsable(account.models.find((item) => item.id === account.default_model_id)),
    )
    .map((account) => ({
      value: account.id,
      label: `${providerName(account.provider)} · ${account.name}`,
    }));
  const action = useMutation({
    mutationFn: (value: CommandAction) => {
      switch (value) {
        case 'start':
          return commands.start(user, instance.id);
        case 'stop':
          return commands.stop(user, instance.id);
        case 'delete':
          return commands.delete(user, instance.id, name);
        case 'configure':
          if (!usable || !choice) throw new APIError(409, 'CONFIGURATION_INVALID');
          return commands.configure(user, instance.id, {
            expected_instance_version: version,
            model_configuration_id: choice.id,
            model_configuration_version: choice.version,
          });
        default:
          throw new Error('操作不可用');
      }
    },
    ...result,
    onSuccess: (accepted: Acceptance) => {
      setDialog(null);
      setName('');
      result.onSuccess(accepted);
    },
  });
  const resume = useMutation({
    mutationFn: (value: CommandAction) => commands.confirmPending(user, value, instance.id),
    ...result,
  });
  const busy = action.isPending || resume.isPending;
  const error = action.error ?? resume.error;
  const actions = instance.allowed_actions.filter((value) =>
    ['start', 'stop', 'delete', 'configure'].includes(value),
  );
  const unresolved = (['start', 'stop', 'delete', 'configure'] as CommandAction[]).filter((value) =>
    pending(user, value, instance.id),
  );
  const open = (value: 'delete' | 'configure') => {
    setVersion(instance.version);
    setSelectedProvider(undefined);
    setName('');
    action.reset();
    resume.reset();
    setDialog(value);
  };
  return (
    <>
      <Space wrap style={{ marginBottom: 16 }}>
        {actions.includes('start') && (
          <Button disabled={busy || unresolved.length > 0} onClick={() => action.mutate('start')}>
            启动实例
          </Button>
        )}
        {actions.includes('stop') && (
          <Button disabled={busy || unresolved.length > 0} onClick={() => action.mutate('stop')}>
            停止实例
          </Button>
        )}
        {actions.includes('configure') && (
          <Button disabled={busy || unresolved.length > 0} onClick={() => open('configure')}>
            更新模型提供商
          </Button>
        )}
        {actions.includes('delete') && (
          <Button danger disabled={busy || unresolved.length > 0} onClick={() => open('delete')}>
            删除实例
          </Button>
        )}
        {unresolved.map((value) => (
          <Button
            key={value}
            loading={resume.isPending}
            disabled={busy && !resume.isPending}
            onClick={() => resume.mutate(value)}
          >
            确认上次
            {
              (
                {
                  start: '启动',
                  stop: '停止',
                  delete: '删除',
                  configure: '模型提供商更新',
                } as Record<string, string>
              )[value]
            }
            结果
          </Button>
        ))}
      </Space>
      {error && (
        <Alert style={{ marginBottom: 16 }} type="error" showIcon message={error.message} />
      )}
      <DeleteConfirmation
        open={dialog === 'delete'}
        title="删除实例"
        name={instance.name}
        label="确认实例名称"
        value={name}
        onChange={setName}
        busy={busy}
        error={action.error?.message}
        onCancel={() => setDialog(null)}
        onConfirm={() => action.mutate('delete')}
      >
        <p>
          删除后将<strong>释放计算资源</strong>。
        </p>
        <p>
          工作数据按保留策略保存，<strong>仍占用存储额度</strong>。
        </p>
      </DeleteConfirmation>
      <Modal
        className="instance-model-update"
        title="更新模型提供商"
        open={dialog === 'configure'}
        onCancel={() => {
          if (!busy) setDialog(null);
        }}
        closable={!busy}
        maskClosable={!busy}
        keyboard={!busy}
        cancelButtonProps={{ disabled: busy }}
        confirmLoading={busy}
        okText="应用配置"
        okButtonProps={{ disabled: busy || !usable }}
        onOk={() => {
          if (!busy && usable) action.mutate('configure');
        }}
      >
        <p className="instance-model-update-intro">
          为「{instance.name}」选择模型提供商，应用该账号的默认模型。
        </p>
        <div className="instance-model-comparison" aria-label="模型更新预览">
          <div className="instance-model-comparison-item">
            <span>当前模型</span>
            <strong>{instance.model_display_name || '模型信息暂不可用'}</strong>
          </div>
          <ArrowRight className="instance-model-comparison-arrow" size={18} aria-hidden="true" />
          <div className="instance-model-comparison-item" role="status">
            <span>应用后的模型{choice && sameModel && <small>与当前相同</small>}</span>
            {choice && usable ? (
              <strong>{choice.model}</strong>
            ) : (
              <span className="instance-model-placeholder">选择提供商后显示</span>
            )}
          </div>
        </div>
        <Form layout="vertical">
          <Form.Item
            label="模型提供商"
            htmlFor="instance-update-provider"
            required
            extra="仅显示已完成校验的账号，使用其默认模型。"
          >
            <Select
              id="instance-update-provider"
              aria-label="应用到实例的模型提供商"
              showSearch
              optionFilterProp="label"
              style={{ width: '100%' }}
              placeholder="搜索并选择模型提供商"
              notFoundContent="暂无可用的模型提供商"
              value={account ? selectedProvider : undefined}
              loading={canReadModels && accounts.isPending}
              disabled={busy || !canReadModels || accounts.isError}
              options={providerOptions}
              optionRender={(option) => {
                const item = activeAccounts.find((item) => item.id === option.value);
                return (
                  <div className="instance-provider-option">
                    <ProviderLogo provider={item?.provider ?? 'custom'} size={28} />
                    <span>
                      <strong>{option.label}</strong>
                      <small>
                        默认模型 ·{' '}
                        {item?.models.find((model) => model.id === item.default_model_id)?.model}
                      </small>
                    </span>
                  </div>
                );
              }}
              labelRender={(option) => (
                <ProviderLabel
                  provider={
                    activeAccounts.find((item) => item.id === option.value)?.provider ?? 'custom'
                  }
                >
                  {option.label}
                </ProviderLabel>
              )}
              onChange={(value) => {
                setSelectedProvider(value);
                action.reset();
              }}
            />
          </Form.Item>
        </Form>
        <Alert
          className="instance-model-restart-notice"
          type="info"
          showIcon
          message={
            instance.observed_state === 'RUNNING'
              ? '应用后实例将重启，工作数据保留。'
              : '应用后将在下次启动时使用新模型，工作数据保留。'
          }
        />
        {!canReadModels ? (
          <Alert type="warning" showIcon message="没有模型提供商读取权限" />
        ) : accounts.isError ? (
          <Alert
            type="error"
            showIcon
            message="模型提供商账号加载失败"
            action={<Button onClick={() => void accounts.refetch()}>重新加载</Button>}
          />
        ) : !accounts.isPending && !providerOptions.length ? (
          <Alert type="warning" showIcon message="暂无可用的模型提供商，请先配置并完成校验" />
        ) : (
          selectedProvider &&
          !usable && (
            <Alert type="warning" showIcon message="所选提供商暂不可用，请重新选择或等待校验完成" />
          )
        )}
        {action.isError && (
          <Alert style={{ marginTop: 16 }} type="error" message={action.error.message} />
        )}
      </Modal>
    </>
  );
}

export { CreateInstanceButton } from './create-instance-button';

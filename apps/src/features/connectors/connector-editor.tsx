import { useQueryClient } from '@tanstack/react-query';
import { Alert, Button, Collapse, Form, Input, Modal, Select, Switch } from 'antd';
import { ArrowLeft } from 'lucide-react';
import { useState } from 'react';
import { type Connector, type ConnectorPreset, type MCPEndpoint } from '../../api/connectors';
import { APIError } from '../../api/error';
import { type CurrentUser } from '../../api/identity';
import { expireSession } from '../../shared/hooks/use-expired-session';
import { ConnectorPresetSetup } from './connector-catalog';
import { authorizeConnector } from './connector-oauth';
import { client, errorText } from './connector-shared';

export function ConnectorEditor({
  user,
  value,
  duplicate,
  preset,
  endpoints: allEndpoints,
  onBack,
  onClose,
  onSaved,
}: {
  user: CurrentUser;
  value: Connector | 'new';
  duplicate: Connector | null;
  preset: ConnectorPreset | null;
  endpoints: MCPEndpoint[];
  onBack?: () => void;
  onClose: () => void;
  onSaved: () => Promise<void>;
}) {
  const cache = useQueryClient();
  const [form] = Form.useForm();
  const currentURL = Form.useWatch('url', form);
  const authType = Form.useWatch('auth_type', form);
  const enabled = Form.useWatch('enabled', form);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState<Connector | null>(null);
  const creating = value === 'new';
  const previous = creating ? duplicate : value;
  const address =
    previous?.url ?? allEndpoints.find((e) => e.id === previous?.endpoint_id)?.url ?? '';
  const target = {
    url: previous ? address : (preset?.default_url ?? ''),
  };
  const official = !!preset?.default_url && currentURL === preset.default_url;
  const native = official && preset?.auth_kind !== 'oauth2';
  const lexiang = native && preset?.id === 'tencent-lexiang';
  const defaultAuth =
    target.url === preset?.default_url
      ? preset?.auth_kind === 'oauth2'
        ? 'oauth2'
        : 'bearer'
      : 'none';
  const initial = {
    ...(previous
      ? {
          name: previous.name + (creating ? ' 副本' : ''),
          description: previous.description,
          enabled: previous.enabled,
          auth_type: previous.auth_type,
        }
      : {
          enabled: true,
          auth_type: defaultAuth,
          ...(preset ? { name: preset.name, description: preset.description } : {}),
        }),
    ...target,
    credential: '',
  };
  async function save() {
    try {
      const { company_from, ...fields } = await form.validateFields();
      const credential =
        fields.auth_type === 'bearer'
          ? lexiang && fields.credential
            ? JSON.stringify({ token: fields.credential, company_from })
            : (fields.credential ?? '')
          : '';
      setBusy(true);
      setError('');
      const existing = creating ? saved : value;
      const connection = await client.request<Connector>(
        user,
        existing ? `/${existing.id}` : '',
        {
          ...fields,
          protocol_version: previous?.protocol_version ?? '2026-07-28',
          credential,
          catalog_id: preset?.id ?? '',
          expected_version: existing?.version ?? 0,
        },
        existing ? 'PUT' : 'POST',
      );
      setSaved(connection);
      if (fields.auth_type === 'oauth2' && fields.enabled) {
        await authorizeConnector(user, connection);
      } else {
        await onSaved();
      }
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 401) expireSession(cache);
      if (cause instanceof Error) setError(errorText(cause));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      className="connector-modal connector-editor"
      width={600}
      open
      title={creating ? '添加连接器' : '连接器设置'}
      okText={authType === 'oauth2' && enabled ? '授权连接' : '保存'}
      cancelText="取消"
      confirmLoading={busy}
      onCancel={() => {
        if (!busy) onClose();
      }}
      onOk={() => void save()}
    >
      {onBack && (
        <Button
          className="connector-editor-back"
          type="text"
          icon={<ArrowLeft size={15} aria-hidden="true" />}
          disabled={busy}
          onClick={onBack}
        >
          返回选择
        </Button>
      )}
      {error && <Alert type="error" role="alert" showIcon message={error} />}
      {preset && <ConnectorPresetSetup preset={preset} />}{' '}
      <Form form={form} layout="vertical" initialValues={initial} disabled={busy}>
        <Form.Item
          name="name"
          label="连接器名称"
          rules={[{ required: true, whitespace: true, max: 100, message: '请输入1～100字符名称' }]}
        >
          <Input maxLength={100} />
        </Form.Item>
        <Form.Item
          name="url"
          label="服务地址"
          extra={
            official ? (
              '已预填官方 MCP 地址，可按需修改。'
            ) : preset && !preset.default_url ? (
              <>
                {preset.id === 'dify'
                  ? '此地址由你的 Dify 应用或工作流发布后生成。'
                  : preset.mode === 'gateway' || preset.mode === 'stdio'
                    ? '此服务没有统一的公共 MCP 地址，请填写你部署的 MCP 网关地址。'
                    : '此服务需通过兼容网关接入，请填写网关地址。'}{' '}
                <a href={preset.guide_url} target="_blank" rel="noopener noreferrer">
                  获取服务地址
                </a>
              </>
            ) : (
              '填写此连接器的 HTTPS MCP 服务地址。'
            )
          }
          rules={[
            { required: true, whitespace: true, message: '请输入 MCP 服务地址' },
            { type: 'url', message: '请输入完整的服务地址' },
          ]}
        >
          <Input maxLength={1000} placeholder="https://mcp.example.com/mcp" />
        </Form.Item>
        <Form.Item name="auth_type" label="认证方式">
          <Select
            virtual={false}
            options={[
              { value: 'none', label: '无需认证' },
              { value: 'bearer', label: native ? 'API Key / Token' : 'Bearer Token' },
              { value: 'oauth2', label: 'OAuth 2.0 授权' },
            ]}
          />
        </Form.Item>
        {lexiang && authType === 'bearer' && (
          <Form.Item
            name="company_from"
            label="企业标识 company_from"
            extra="从乐享 MCP 配置页获取；更换访问令牌时须一起填写，留空可保留原凭据。"
            dependencies={['credential']}
            rules={[
              {
                validator: (_, company) =>
                  (!creating &&
                    previous?.auth_type === 'bearer' &&
                    currentURL === address &&
                    !form.getFieldValue('credential')) ||
                  (typeof company === 'string' && !!company.trim())
                    ? Promise.resolve()
                    : Promise.reject(new Error('请输入乐享企业标识')),
              },
            ]}
          >
            <Input maxLength={256} autoComplete="off" />
          </Form.Item>
        )}
        <Form.Item noStyle shouldUpdate>
          {() => {
            const currentAddress = form.getFieldValue('url');
            return form.getFieldValue('auth_type') === 'bearer' ? (
              <Form.Item
                name="credential"
                label="服务凭据"
                extra={
                  creating
                    ? '凭据加密保存，仅本人可使用，不会读回。'
                    : '服务不变时留空保留原凭据；更换服务须重新填写。'
                }
                rules={
                  creating ||
                  previous?.auth_type !== 'bearer' ||
                  currentAddress !== address ||
                  (lexiang && !!form.getFieldValue('company_from'))
                    ? [
                        {
                          required: true,
                          message: native
                            ? '请输入此服务的 Token 或 API Key'
                            : '请输入此服务的 Bearer 凭据',
                        },
                      ]
                    : []
                }
              >
                <Input.Password autoComplete="new-password" maxLength={4096} />
              </Form.Item>
            ) : null;
          }}
        </Form.Item>
        <Collapse
          className="connector-editor-advanced"
          ghost
          items={[
            {
              key: 'advanced',
              label: '更多设置',
              forceRender: true,
              children: (
                <Form.Item name="description" label="说明">
                  <Input.TextArea rows={2} maxLength={1000} />
                </Form.Item>
              ),
            },
          ]}
        />
        <div className="connector-editor-enabled">
          <label htmlFor="connector-enabled">启用连接器</label>
          <Form.Item name="enabled" valuePropName="checked">
            <Switch id="connector-enabled" />
          </Form.Item>
        </div>
      </Form>
      <p className="connector-editor-note">
        {authType === 'oauth2'
          ? '仅本人可见。授权后自动连接并启用发现的工具。'
          : '仅本人可见。保存后自动连接并启用发现的工具。'}
      </p>
    </Modal>
  );
}

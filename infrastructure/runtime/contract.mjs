import { isIP } from 'node:net';
const uuid = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/;
const fields = ['schema_version', 'instance_id', 'org_id', 'owner_id', 'asset_id', 'runtime_generation', 'configuration_version', 'public_url', 'endpoint', 'model'];
export function validateConfiguration(value) {
  const extras = ['provider', 'protocol', 'route_revision', 'approval_digest', 'context_window', 'max_output_tokens', 'response_models', 'approved_ips', 'approved_networks', 'ca_pem'];
  const allowed = value?.schema_version === 2 ? [...fields, ...extras] : fields;
  if (!value || typeof value !== 'object' || Array.isArray(value) || Object.keys(value).some(key => !allowed.includes(key)) || fields.some(key => !(key in value)) || ![1, 2].includes(value.schema_version)) throw new Error('Invalid runtime configuration');
  for (const key of ['instance_id', 'org_id', 'owner_id', 'asset_id', 'runtime_generation']) if (!uuid.test(value[key])) throw new Error('Invalid runtime identity');
  if (!Number.isSafeInteger(value.configuration_version) || value.configuration_version < 1 || typeof value.model !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$/.test(value.model)) throw new Error('Invalid model configuration');
  for (const key of ['public_url', 'endpoint']) {
    const url = new URL(value[key]);
    if (url.protocol !== 'https:' || !url.hostname || url.username || url.password || url.search || url.hash || value[key].length > 2048) throw new Error('Invalid runtime URL');
    if (key === 'public_url' && url.pathname !== '/') throw new Error('Runtime requires a root public URL');
  }
  if (value.schema_version === 2) {
    if (value.provider !== 'platform-models' || value.protocol !== 'anthropic-messages' || typeof value.route_revision !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$/.test(value.route_revision) || !/^sha256:[a-f0-9]{64}$/.test(value.approval_digest) || new URL(value.endpoint).pathname !== '/') throw new Error('Invalid external model contract');
    if (!Number.isSafeInteger(value.context_window) || value.context_window < 1024 || !Number.isSafeInteger(value.max_output_tokens) || value.max_output_tokens < 128 || value.max_output_tokens > value.context_window) throw new Error('Invalid model capabilities');
    if (!Array.isArray(value.response_models) || !value.response_models.length || value.response_models.length > 16 || value.response_models.some(id => typeof id !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$/.test(id))) throw new Error('Invalid response model approval');
    if (!Array.isArray(value.approved_ips) || !value.approved_ips.length || value.approved_ips.length > 256 || value.approved_ips.some(ip => isIP(ip) !== 4) || !Array.isArray(value.approved_networks) || !value.approved_networks.length || value.approved_networks.length > 16) throw new Error('Invalid model network approval');
    if (value.ca_pem !== undefined && (typeof value.ca_pem !== 'string' || value.ca_pem.length > 131072)) throw new Error('Invalid model CA');
  }
  return { ...value };
}
export function profilePatch(config, bootstrapPath) {
  validateConfiguration(config);
  const external = config.schema_version === 2;
  return [
    { id: 'web-runtime', disabled: false, config: { openBrowser: false, printUrl: false, publicUrl: config.public_url, surfaceContext: true } },
    { id: 'connection', disabled: false },
    { id: 'agent-default-model', disabled: false, config: { provider: external ? 'platform-models' : 'deepseek-official', model: config.model } },
    ...(external ? [{ id: 'llm-deepseek', disabled: true }, { id: 'llm-pi-ai', disabled: true }, { insert: [{ id: 'harness-cloud-models', name: new URL('./external-model.mjs', 'file://' + bootstrapPath).pathname }] }] : [{ id: 'llm-deepseek', config: { baseURL: config.endpoint, apiKeyEnv: 'HC_DSH_MODEL_KEY', maxTokens: 4096, thinking: 'disabled', reasoningEffort: 'off', streamIdleTimeoutMs: 30000, retryPolicy: { mode: 'normal', maxRetries: 1 }, models: [{ id: config.model, name: config.model, contextWindow: 1000000, maxTokens: 4096 }] } }]),
    { id: 'hmr', disabled: false },
    { id: 'config-editor', disabled: false },
    { id: 'settings', disabled: false },
    { id: 'plugin-manager', disabled: false, config: { pnpmCommand: '/opt/harness-runtime/node_modules/.bin/pnpm', inspectTimeoutMs: 20000, idleTimeoutMs: 120000 } },
    ...['tool-plugin-manager', 'llm-deepseek-account', 'session-log-deepseek', 'plugin-package-inventory-deepseek'].map(id => ({ id, disabled: true })),
    { insert: [{ id: 'harness-cloud-bootstrap', name: bootstrapPath }] },
  ];
}

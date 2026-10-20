import { test } from 'node:test';
import assert from 'node:assert/strict';
import { validateConfiguration, profilePatch } from '../contract.mjs';

const config = { schema_version: 1, instance_id: '00000000-0000-4000-8000-000000000001', org_id: '00000000-0000-4000-8000-000000000002', owner_id: '00000000-0000-4000-8000-000000000003', asset_id: '00000000-0000-4000-8000-000000000004', runtime_generation: '00000000-0000-4000-8000-000000000005', configuration_version: 1, public_url: 'https://instance.example.test', endpoint: 'https://api.deepseek.com/anthropic', model: 'deepseek-flash' };
test('runtime contract refuses unexpected fields and invalid native authorities', () => {
  assert.deepEqual(validateConfiguration(config), config);
  for (const value of [{ ...config, api_key: 'must-not-be-in-config' }, { ...config, public_url: 'https://instance.example.test/?token=secret' }, { ...config, public_url: 'https://evil.example/subpath' }, { ...config, endpoint: 'http://127.0.0.1' }, { ...config, configuration_version: 0 }]) assert.throws(() => validateConfiguration(value));
});
test('profile preserves authentication and never inlines a model credential', () => {
  const patch = profilePatch(config, '/platform/bootstrap.mjs');
  assert.equal(patch.find(row => row.id === 'web-runtime').config.printUrl, false);
  assert.equal(patch.find(row => row.id === 'agent-default-model').config.model, 'deepseek-flash');
  assert.equal(patch.find(row => row.id === 'llm-deepseek').config.apiKeyEnv, 'HC_DSH_MODEL_KEY');
  assert.ok(!JSON.stringify(patch).includes('synthetic-test-key'));
  assert.ok(!patch.some(row => row.id === 'connection' && row.disabled));
});
test('normal browser agents use a bounded model default and bounded retry policy', () => {
  const provider = profilePatch(config, '/platform/bootstrap.mjs').find(row => row.id === 'llm-deepseek').config;
  assert.equal(provider.maxTokens, 4096);
  assert.equal(provider.models[0].maxTokens, 4096);
  assert.equal(provider.thinking, 'disabled');
  assert.equal(provider.reasoningEffort, 'off');
  assert.deepEqual(provider.retryPolicy, { mode: 'normal', maxRetries: 1 });
  assert.equal(provider.streamIdleTimeoutMs, 30000);
});

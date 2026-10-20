import { test } from 'node:test';
import assert from 'node:assert/strict';
import { prepareManagedCreator } from '../bootstrap.mjs';

test('managed Creator disables only the unavailable package manager tool and preserves its composition', async () => {
 const condition = { expression: 'native platform condition' };
 const plugins = [{ id: 'tool-cordis', name: '@deepseek-ai/dsh-tool-cordis' }, { id: 'tool-bash', disabled: condition }, { id: 'tool-plugin-manager', name: '@deepseek-ai/dsh-plugin-manager/tools', disabled: condition }];
 const original = { id: 'cordis', order: 4, plugins }; let updates = 0;
 const entry = { options: { id: 'preset-cordis', config: original }, async update(value) { updates++; this.options.config = value.config; } };
 const ctx = { loader: { entries: () => [entry], await: async () => {} }, agentPresets: { async resolve(id) { assert.equal(id, 'cordis'); assert.equal(entry.options.config.plugins[2].disabled, true); return { id }; } } };
 await prepareManagedCreator(ctx);
 assert.equal(updates, 1); assert.equal(entry.options.config.order, 4); assert.equal(entry.options.config.plugins[0], plugins[0]); assert.equal(entry.options.config.plugins[1], plugins[1]); assert.equal(plugins[2].disabled, condition);
 await prepareManagedCreator(ctx); assert.equal(updates, 1);
});
test('missing or broken Creator never passes initialization', async () => {
 await assert.rejects(prepareManagedCreator({ loader: { entries: () => [] } }), /declaration unavailable/);
 const entry = { options: { id: 'preset-cordis', config: { plugins: [] } } };
 await assert.rejects(prepareManagedCreator({ loader: { entries: () => [entry], await: async () => {} }, agentPresets: { resolve: async () => ({ broken: 'required tool missing' }) } }), /initialization unavailable/);
});
test('persistent profile keeps the native Creator package manager tool', async () => {
 const plugins = [{ name: '@deepseek-ai/dsh-plugin-manager/tools' }];
 const entry = { options: { id: 'preset-cordis', config: { plugins } }, update() { assert.fail('Native Creator configuration must be preserved'); } };
 await prepareManagedCreator({ get: name => name === 'pluginManager' ? {} : undefined, loader: { entries: () => [entry] }, agentPresets: { resolve: async () => ({ id: 'cordis' }) } });
 assert.equal(plugins[0].disabled, undefined);
});

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { serializeMessages, translateMessages, sseEvents } from '../messages-wire.mjs';
import { accountModels } from '../external-model.mjs';

test('account directory switches models with their own capabilities and response approval', async () => {
  const first = { id: 'hc-first-v1', name: 'provider-fast', context_window: 32768, max_output_tokens: 1024, response_models: ['provider-fast'] };
  const second = { id: 'hc-second-v1', name: 'provider-pro', context_window: 128000, max_output_tokens: 8192, response_models: ['provider-pro-response'] };
  const models = accountModels({ object: 'list', data: [first, second] });
  const request = serializeMessages({ model: second.id, messages: [{ role: 'user', content: [{ type: 'text', text: 'hello' }] }], maxTokens: 8000 }, models[1]);
  assert.equal(request.model, second.id);
  assert.equal(request.max_tokens, 8000);
  assert.throws(() => serializeMessages({ model: first.id, messages: [], maxTokens: 8000 }, models[0]));
  assert.throws(() => accountModels({ object: 'list', data: [first, first] }));
  assert.throws(() => accountModels({ object: 'list', data: [{ ...second, max_output_tokens: 999999 }] }));
  assert.throws(() => accountModels({ object: 'list', data: [{ ...second, response_models: [] }] }));
  assert.deepEqual(accountModels({ object: 'list', data: [] }), []);
});

const config = { model: 'enterprise-chat-v1', max_output_tokens: 4096, response_models: ['provider-chat-2026'] };
test('external model serialization preserves tool calls/results without DeepSeek parameters', () => {
  const request = serializeMessages({ provider: 'platform-models', model: config.model, messages: [{ role: 'system', content: [{ type: 'text', text: 'system' }] }, { role: 'user', content: [{ type: 'text', text: 'write a file' }] }, { role: 'assistant', content: [{ type: 'tool-call', id: 'call-1', name: 'write', arguments: '{"text":"value"}' }] }, { role: 'tool', toolCallId: 'call-1', content: [{ type: 'text', text: 'written' }] }], tools: [{ name: 'write', description: 'write file', parameters: { type: 'object' } }] }, config);
  assert.equal(request.system, 'system'); assert.equal(request.stream, true); assert.equal(request.max_tokens, 4096);
  assert.deepEqual(request.messages[1].content[0].input, { text: 'value' });
  assert.equal(request.messages[2].content[0].tool_use_id, 'call-1');
  assert.equal(request.thinking, undefined); assert.equal(request.reasoning_effort, undefined);
  assert.throws(() => serializeMessages({ model: 'unapproved', messages: [] }, config));
});
test('stream translation checks response mapping, fragmented JSON, final marker and usage order', async () => {
  const events = [
    { type: 'message_start', message: { model: config.response_models[0], usage: { input_tokens: 5 } } },
    { type: 'content_block_start', index: 0, content_block: { type: 'tool_use', id: 'call-1', name: 'write', input: {} } },
    { type: 'content_block_delta', index: 0, delta: { type: 'input_json_delta', partial_json: '{"text":' } },
    { type: 'content_block_delta', index: 0, delta: { type: 'input_json_delta', partial_json: '"value"}' } },
    { type: 'content_block_stop', index: 0 },
    { type: 'message_delta', delta: { stop_reason: 'tool_use' }, usage: { output_tokens: 10 } },
    { type: 'message_stop' },
  ];
  const collect = async source => { const result = []; for await (const chunk of translateMessages(source, config)) result.push(chunk); return result; };
  const chunks = await collect(events);
  assert.equal(chunks.find(chunk => chunk.type === 'block-end').block.arguments, '{"text":"value"}');
  assert.equal(chunks.at(-2).type, 'usage'); assert.deepEqual(chunks.at(-1).reason, { kind: 'tool-calls' });
  await assert.rejects(collect(events.slice(0, -1)));
  await assert.rejects(collect([{ ...events[0], message: { model: 'unapproved' } }, ...events.slice(1)]));
  const source = new Response(events.map(event => `event: ${event.type}\ndata: ${JSON.stringify(event)}\n\n`).join(''));
  assert.deepEqual(await collect(sseEvents(source.body)), chunks);
});

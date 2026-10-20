// The public DSH LlmAdapter contract is pinned with the runtime npm artifact.
// This adapter deliberately implements text and tool Messages only.
function fail(code = 'MALFORMED_RESPONSE') { throw Object.assign(new Error('External model protocol unavailable'), { code }); }
function object(value) { if (!value || typeof value !== 'object' || Array.isArray(value)) fail(); return value; }
function text(value) { if (typeof value !== 'string') fail(); return value; }

export function serializeMessages(options, config) {
  if (options.model !== config.model || options.reasoningEffort !== undefined) fail('UNSUPPORTED_OPTION');
  const messages = []; let system = options.system ?? '';
  for (const message of options.messages) {
    if (message.role === 'system') {
      if (message.content.some(block => block.type !== 'text')) fail('UNSUPPORTED_CONTENT');
      system = message.content.map(block => block.text).join(''); continue;
    }
    if (!['user', 'assistant', 'tool'].includes(message.role)) fail('UNSUPPORTED_CONTENT');
    let content = message.content.map(block => {
      if (block.type === 'text') return { type: 'text', text: block.text };
      if (message.role === 'assistant' && block.type === 'tool-call') {
        let input; try { input = JSON.parse(block.arguments); } catch { fail('INVALID_REQUEST'); }
        return { type: 'tool_use', id: block.id, name: block.name, input: object(input) };
      }
      fail('UNSUPPORTED_CONTENT');
    });
    const role = message.role === 'tool' ? 'user' : message.role;
    if (message.role === 'tool') {
      if (!message.toolCallId) fail('INVALID_REQUEST');
      content = [{ type: 'tool_result', tool_use_id: message.toolCallId, content, ...(message.isError ? { is_error: true } : {}) }];
    }
    if (!content.length) continue;
    if (messages.at(-1)?.role === role) messages.at(-1).content.push(...content);
    else messages.push({ role, content });
  }
  const maxTokens = options.maxTokens ?? Math.min(4096, config.max_output_tokens);
  if (!Number.isSafeInteger(maxTokens) || maxTokens < 1 || maxTokens > config.max_output_tokens) fail('UNSUPPORTED_OPTION');
  return { model: config.model, max_tokens: maxTokens, stream: true, messages,
    ...(system ? { system } : {}),
    ...(options.tools?.length ? { tools: options.tools.map(tool => ({ name: tool.name, description: tool.description, input_schema: tool.parameters })) } : {}),
    ...(options.temperature === undefined ? {} : { temperature: options.temperature }),
    ...(options.stop === undefined ? {} : { stop_sequences: options.stop }),
  };
}

export async function* sseEvents(body) {
  const decoder = new TextDecoder('utf-8', { fatal: true }); let pending = '', event = '', data = [], total = 0;
  for await (const chunk of body) {
    total += chunk.byteLength; if (total > 16 * 1024 * 1024) fail();
    pending += decoder.decode(chunk, { stream: true });
    let newline;
    while ((newline = pending.indexOf('\n')) >= 0) {
      let line = pending.slice(0, newline); pending = pending.slice(newline + 1);
      if (line.endsWith('\r')) line = line.slice(0, -1);
      if (line.length > 1024 * 1024) fail();
      if (!line) {
        if (data.length) { let parsed; try { parsed = JSON.parse(data.join('\n')); } catch { fail(); } if (parsed.type !== event) fail(); yield parsed; }
        data = []; event = '';
      } else if (line.startsWith('event:')) { if (event) fail(); event = line.slice(6).trim(); }
      else if (line.startsWith('data:')) data.push(line.slice(5).trimStart());
      else if (!line.startsWith(':')) fail();
    }
    if (pending.length > 1024 * 1024 || data.join('').length > 1024 * 1024) fail();
  }
  pending += decoder.decode();
  if (pending.trim() || data.length || event) fail();
}

export async function* translateMessages(events, config) {
  const blocks = []; const usage = { inputTokens: 0, outputTokens: 0 };
  let started = false, ended = false, open = -1, reason;
  const updateUsage = value => {
    for (const [wire, local] of Object.entries({ input_tokens: 'inputTokens', output_tokens: 'outputTokens', cache_read_input_tokens: 'cacheReadTokens', cache_creation_input_tokens: 'cacheWriteTokens' })) {
      if (value?.[wire] === undefined) continue;
      if (!Number.isSafeInteger(value[wire]) || value[wire] < 0) fail();
      usage[local] = value[wire];
    }
  };
  for await (const event of events) {
    if (event.type === 'ping') continue;
    if (ended) fail();
    if (event.type === 'message_start') {
      if (started || !config.response_models.includes(event.message?.model)) fail();
      started = true; updateUsage(event.message.usage); continue;
    }
    if (!started) fail();
    switch (event.type) {
      case 'content_block_start': {
        if (open !== -1 || reason || event.index !== blocks.length || blocks.length >= 1024) fail();
        const wire = object(event.content_block); let block;
        if (wire.type === 'text') block = { type: 'text', text: text(wire.text) };
        else if (wire.type === 'tool_use') {
          if (!wire.id || !wire.name) fail();
          block = { type: 'tool-call', id: text(wire.id), name: text(wire.name), arguments: JSON.stringify(object(wire.input)) };
        } else fail('UNSUPPORTED_CONTENT');
        open = event.index; blocks.push({ block, partial: '' });
        yield { type: 'block-start', index: open, blockType: block.type };
        if (block.type === 'text' && block.text) yield { type: 'text-delta', index: open, text: block.text };
        if (block.type === 'tool-call') yield { type: 'tool-call-delta', index: open, id: block.id, name: block.name, argumentsDelta: '' };
        break;
      }
      case 'content_block_delta': {
        if (open < 0 || event.index !== open) fail();
        const current = blocks[open], delta = object(event.delta);
        if (delta.type === 'text_delta' && current.block.type === 'text') {
          const value = text(delta.text); current.block.text += value; yield { type: 'text-delta', index: open, text: value };
        } else if (delta.type === 'input_json_delta' && current.block.type === 'tool-call') {
          const value = text(delta.partial_json); current.partial += value; yield { type: 'tool-call-delta', index: open, id: current.block.id, argumentsDelta: value };
        } else fail();
        break;
      }
      case 'content_block_stop': {
        if (open < 0 || event.index !== open) fail();
        const current = blocks[open];
        if (current.partial) { try { object(JSON.parse(current.partial)); } catch { fail(); } current.block.arguments = current.partial; }
        yield { type: 'block-end', index: open, block: current.block }; open = -1; break;
      }
      case 'message_delta':
        if (open !== -1 || reason) fail();
        reason = ({ end_turn: 'stop', stop_sequence: 'stop', tool_use: 'tool-calls', max_tokens: 'max-tokens' })[event.delta?.stop_reason];
        if (!reason) fail(); updateUsage(event.usage); break;
      case 'message_stop': if (open !== -1 || !reason) fail(); ended = true; break;
      default: fail();
    }
  }
  if (!ended || !blocks.length) fail();
  yield { type: 'usage', usage };
  yield { type: 'finish', reason: { kind: reason } };
}

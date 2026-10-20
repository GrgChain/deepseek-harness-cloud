// Explicitly enabled contract fixture, never part of the production profile.
import { readFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import { createUserMessage } from '/opt/harness-runtime/node_modules/@deepseek-ai/dsh-llm/lib/index.js';
import { installModelSelection } from '/opt/harness-runtime/node_modules/@deepseek-ai/dsh-agent/lib/index.js';
import { writePrivate } from '/opt/harness-runtime/private-file.mjs';
export const name = 'hc-real-model-contract';
export const inject = ['agents', 'sessions', 'agentDefaultModel', 'agentPresets'];
export function apply(ctx) {
  let phase = 'READINESS';
  let transcript;
  void (async () => {
    await ctx.get('loader')?.await();
    // The production bootstrap owns initial workspace registration. Exercise
    // tools only after it has published the normal readiness contract.
    for (let attempt = 0; ; attempt++) {
      try { await readFile('/run/harness/native.json'); break; } catch {}
      if (attempt >= 40) throw new Error('Runtime readiness unavailable');
      await new Promise(resolve => setTimeout(resolve, 250));
    }
    let calls = 0;
    phase = 'AGENT_CREATION';
    ctx.on('agent/request', async (_event, next) => {
      if (++calls > 3) throw new Error('Contract request budget exhausted');
      const configuration = await next();
      return { ...configuration, maxTokens: 512, reasoningEffort: 'off' };
    });
    const selection = ctx.agentDefaultModel.currentSelection();
    const preset = await ctx.agentPresets.resolve();
    if (preset.broken) throw new Error('Default Web preset is unavailable');
    const { agent } = await ctx.agents.create({
      sessionId: 'hc-probe-' + randomUUID(), meta: { cwd: '/data/workspace', agentPreset: preset.id },
      agentOptions: { ...selection, maxTokens: 512, reasoningEffort: 'off' },
      async setup(agentCtx) {
        installModelSelection(agentCtx, { current: { ...selection, reasoningEffort: 'off' }, assembled: undefined });
        await ctx.agentPresets.mount(agentCtx, preset.id);
      },
    });
    const timeout = setTimeout(() => agent.cancel({ kind: 'hook', reason: 'Contract deadline' }), 45000);
    try {
      phase = 'TASK';
      agent.followup(createUserMessage({ source: { kind: 'user' }, content: [{ type: 'text', text: 'Use the available local tools to write exactly hc-real-model-ok (no newline) into /data/workspace/hc-real-probe.txt, then read that file using a tool. Return only hc-real-model-ok. Do not use network, subagents, or any other files.' }] }));
      await agent.whenIdle();
      await ctx.sessions.flush(agent.session);
      transcript = agent.session.snapshotEvents(0);
      phase = 'TOOL_PROOF';
      const proof = await readFile('/data/workspace/hc-real-probe.txt', 'utf8');
      if (proof !== 'hc-real-model-ok' || calls < 1 || calls > 3) throw new Error('Tool proof was not produced');
      const results = transcript.filter(event => event.type === 'tool/result').map(event => event.data.message);
      if (!results.some(message => !message.isError && message.content.some(block => block.type === 'text' && block.text.includes(proof)))) throw new Error('File contents were not read back through a tool');
      const final = transcript.filter(event => event.type === 'assistant/message').at(-1)?.data.message;
      const answer = final?.content.filter(block => block.type === 'text').map(block => block.text).join('').trim();
      if (answer !== proof) throw new Error('Model did not complete the bounded task');
      await writePrivate('/run/harness/model-evidence.json', { passed: true, requests: calls, max_output_tokens: 512, model: selection.model, workspace: '/data/workspace', file_written: true, tool_readback: true, final_answer: true });
    } finally { clearTimeout(timeout); }
  })().catch(async error => { await writePrivate('/run/harness/model-evidence.json', { passed: false, phase, diagnostic: String(error), transcript }).catch(() => {}); });
}

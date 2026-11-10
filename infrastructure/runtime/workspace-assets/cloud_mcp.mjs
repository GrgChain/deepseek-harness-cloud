// Use DSH's official MCP result/schema adapter. The platform retains ownership
// of remote credentials and the durable call ledger on its existing channel.
import fs from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { createMcpToolDefinition } from '/opt/harness-runtime/node_modules/@deepseek-ai/dsh-mcp-client/lib/index.js';

export function publicToolName(id, rawName) {
  const server = id.replaceAll('-', '');
  const joined = `mcp__${server}__${rawName}`;
  const normalized = joined.replace(/[^a-zA-Z0-9_-]/g, '_');
  if (normalized === joined && normalized.length <= 64) return joined;
  const hash = createHash('sha256').update(`${server}\0${rawName}`).digest('hex').slice(0, 12);
  return `${normalized.slice(0, 51)}_${hash}`;
}

export async function mountMcpServers(ctx, { channel, generation, items, request }) {
  const allowed = new Set(items.filter((item) => item.kind === 'connector').map((item) => item.id));
  if (!allowed.size) return;
  let previous = '';
  let registrations = [];
  let stopped = false;
  let refreshing = false;
  const clear = () => {
    for (const dispose of registrations.splice(0)) dispose();
  };
  const refresh = async () => {
    if (refreshing || stopped) return;
    refreshing = true;
    try {
      let servers = [];
      try {
        const raw = await fs.readFile(channel + '.hc-mcp-tools.json', 'utf8');
        if (Buffer.byteLength(raw) > 16 * 2 ** 20) throw Error('MCP catalog exceeds limit');
        const view = JSON.parse(raw);
        if (view.generation === generation && view.expires_at > Date.now()) servers = view.servers;
      } catch (error) {
        if (error.code !== 'ENOENT') throw error;
      }
      const signature = JSON.stringify(servers);
      if (signature === previous || stopped) return;
      const definitions = [];
      const names = new Set();
      for (const server of servers) {
        if (!allowed.has(server.id)) continue;
        for (const tool of server.tools) {
          if (!tool.enabled) continue;
          const name = publicToolName(server.id, tool.name);
          if (names.has(name)) throw Error('Duplicate MCP tool');
          names.add(name);
          const risky =
            tool.definition.annotations?.readOnlyHint !== true ||
            tool.definition.annotations?.destructiveHint !== false;
          definitions.push(
            createMcpToolDefinition(ctx, {
              name,
              rawName: tool.name,
              description: `${tool.description || tool.name}\nMCP 服务：${server.name}`,
              inputSchema: tool.input_schema,
              outputSchema: tool.definition.outputSchema,
              taskRequired: tool.definition.execution?.taskSupport === 'required',
              call: async (args, exec) => {
                if (risky) {
                  const approval = ctx.get('approval');
                  if (!approval) throw Error('当前会话不支持调用确认，请在平台连接器页面调用');
                  const outcome = await approval.request({
                    agent: exec.agent,
                    toolName: name,
                    callId: exec.callId,
                    signal: exec.signal,
                    reason: `调用 MCP 服务 ${server.name} 的 ${tool.name}，可能修改外部数据。\n参数：${JSON.stringify(args, null, 2)}`,
                  });
                  if (outcome !== 'allowed-once') throw Error('用户未确认此次外部调用');
                }
                const receipt = JSON.parse(
                  await request(
                    {
                      kind: 'connector',
                      source_id: server.id,
                      expected_version: server.version,
                      catalog_version: server.catalog_version,
                      tool: tool.name,
                      arguments: args,
                      confirmed: true,
                    },
                    exec,
                  ),
                );
                if (receipt.status !== 'SUCCEEDED' || !receipt.result)
                  throw Error(receipt.reason || '调用结果未确认，请核对外部执行结果，不要重复执行');
                return receipt.result;
              },
            }),
          );
        }
      }
      clear();
      try {
        for (const definition of definitions) registrations.push(ctx.tools.register(definition));
        previous = signature;
      } catch (error) {
        clear();
        throw error;
      }
    } catch (error) {
      // Invalid or unreadable catalogs withdraw tools rather than retaining stale grants.
      clear();
      previous = '';
      ctx.logger?.warn?.('MCP tools unavailable; check platform connection status');
    } finally {
      refreshing = false;
    }
  };
  await refresh();
  const timer = setInterval(() => void refresh(), 1000);
  timer.unref?.();
  ctx.effect(
    () => () => {
      stopped = true;
      clearInterval(timer);
      clear();
    },
    'cloud-mcp.servers',
  );
}

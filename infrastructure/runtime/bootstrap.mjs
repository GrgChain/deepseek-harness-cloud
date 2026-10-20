import { dirname, join } from 'node:path';
import { validateConfiguration } from './contract.mjs';
import { readPrivate, writePrivate, privateDirectory } from './private-file.mjs';
export const name = 'harness-cloud-bootstrap';
export const inject = ['connection', 'workspaceRegistry', 'agentDefaultModel', 'agentPresets', 'sessionController', 'sessions', 'webServer', 'pluginManager', 'configEditor', 'settings'];
// Older fixed profiles may lack package management. Preserve the native tool
// when the persistent profile manager is available.
export async function prepareManagedCreator(ctx) {
  const entry = ctx.loader.entries().find(row => row.options.id === 'preset-cordis');
  if (!entry || !Array.isArray(entry.options.config?.plugins)) throw new Error('Creator declaration unavailable');
  const config = entry.options.config;
  const plugins = config.plugins.map(row => row.name === '@deepseek-ai/dsh-plugin-manager/tools' ? { ...row, disabled: true } : row);
  if (!ctx.get?.('pluginManager') && plugins.some((row, index) => row !== config.plugins[index] && config.plugins[index].disabled !== true)) {
    await entry.update({ config: { ...config, plugins } });
    await ctx.loader.await();
  }
  const creator = await ctx.agentPresets.resolve('cordis');
  if (creator.broken) throw new Error('Creator initialization unavailable');
}
export function apply(ctx) {
  let phase = 'SETTLEMENT';
  // Await settlement after this plugin returns, so startup does not wait on
  // itself. The token is obtained through the public Connection service.
  const settled = ctx.get('loader')?.await() ?? Promise.resolve();
  void settled.then(async () => {
    if (!ctx.get('connection') || !ctx.get('webServer')) return;
    phase = 'CREATOR_PRESET';
    await prepareManagedCreator(ctx);
    phase = 'CONFIGURATION';
    const configPath = process.env.HC_RUNTIME_CONFIG;
    const config = validateConfiguration(JSON.parse(await readPrivate(configPath)));
    const runDirectory = dirname(configPath);
    await privateDirectory(runDirectory);
    phase = 'DEFAULT_WORKSPACE';
    const workspace = await ctx.workspaceRegistry.initializeDefault(async () => process.cwd());
    const registered = workspace ?? await ctx.workspaceRegistry.resolveByPath(process.cwd());
    const model = ctx.agentDefaultModel.currentSelection();
    phase = 'DEFAULTS_IDENTITY';
    if (!registered || registered.path !== process.cwd() || model.provider !== (config.schema_version === 2 ? 'platform-models' : 'deepseek-official') || model.model !== config.model) throw new Error('Runtime defaults did not initialize');
    phase = 'DEFAULT_SESSION';
    if (registered.sessionIds.length === 0) {
      const sessionId = 'harness-initial-' + config.instance_id;
      const created = await ctx.sessionController.create({ workspaceId: registered.id, sessionId });
      const session = ctx.sessions.get(created.sessionId);
      if (created.sessionId !== sessionId || !session || session.header.cwd !== registered.path || !registered.sessionIds.includes(sessionId)) throw new Error('Runtime initial session did not initialize');
    }
    phase = 'NATIVE_AUTHENTICATION';
    const token = new URL(ctx.connection.authenticatedUrl(config.public_url)).searchParams.get('token');
    if (!token || token.length > 1024) throw new Error('Native authentication channel unavailable');
    phase = 'READINESS_CHANNEL';
    await writePrivate(join(runDirectory, 'native.json'), { schema_version: 1, instance_id: config.instance_id, runtime_generation: config.runtime_generation, configuration_version: config.configuration_version, workspace_id: registered.id, workspace_path: registered.path, provider: model.provider, model: model.model, token });
  }).catch(() => { console.error(`harness-cloud: runtime initialization failed [${phase}]`); process.exitCode = 1; process.kill(process.pid, 'SIGTERM'); });
}

import https from 'node:https';
import tls from 'node:tls';
import dns from 'node:dns';
import { readPrivate } from './private-file.mjs';
import { validateConfiguration } from './contract.mjs';
import { serializeMessages, sseEvents, translateMessages } from './messages-wire.mjs';
export const name = 'harness-cloud-models';
export const inject = ['llm'];

// Metadata comes only from the approved gateway; upstream credentials stay there.
export function accountModels(payload) {
  if (payload?.object !== 'list' || !Array.isArray(payload.data) || payload.data.length > 21) throw new Error('Invalid model directory');
  const seen = new Set();
  return payload.data.map(item => {
    const id = /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$/;
    if (!item || typeof item.id !== 'string' || !id.test(item.id) || seen.has(item.id) || typeof item.name !== 'string' || !id.test(item.name) ||
      !Number.isSafeInteger(item.context_window) || item.context_window < 1024 || item.context_window > 10000000 ||
      !Number.isSafeInteger(item.max_output_tokens) || item.max_output_tokens < 128 || item.max_output_tokens > item.context_window ||
      !Array.isArray(item.response_models) || !item.response_models.length || item.response_models.length > 16 || item.response_models.some(model => typeof model !== 'string' || !id.test(model))) throw new Error('Invalid model directory');
    seen.add(item.id);
    return { ...item, model: item.id };
  });
}

export async function apply(ctx) {
  const { LlmAdapter, LlmError } = await import('@deepseek-ai/dsh-llm');
  const { launchEnvironmentOf } = await import('@deepseek-ai/dsh-launch-environment');
  const config = validateConfiguration(JSON.parse(await readPrivate(process.env.HC_RUNTIME_CONFIG)));
  if (config.schema_version !== 2 || config.provider !== 'platform-models') throw new Error('External model configuration unavailable');
  const agent = new https.Agent({ keepAlive: false, maxSockets: 2, minVersion: 'TLSv1.2', ca: config.ca_pem ? [...tls.rootCertificates, config.ca_pem] : undefined,
    lookup(hostname, options, callback) {
      if (hostname !== new URL(config.endpoint).hostname) return callback(new Error('Model destination rejected'));
      dns.lookup(hostname, { all: true }, (error, addresses) => {
        if (error || !addresses.length || addresses.some(item => !config.approved_ips.includes(item.address))) return callback(new Error('Model DNS rejected'));
        const address = addresses.find(item => item.family === 4);
        if (!address) return callback(new Error('Model IPv4 destination unavailable'));
        callback(null, options.all ? [address] : address.address, address.family);
      });
    },
  });
  ctx.effect(() => () => agent.destroy());
  const credential = () => {
    const key = launchEnvironmentOf(ctx).get('HC_DSH_MODEL_KEY')?.value;
    if (!key) throw new LlmError('Model credential unavailable', 'MISSING_CREDENTIAL');
    return key;
  };
  const directory = async () => {
    let request, response;
    try {
      response = await new Promise((resolve, reject) => {
        request = https.get(config.endpoint + '/v1/models?scope=provider-account', { agent, signal: AbortSignal.timeout(10000), headers: { Authorization: 'Bearer ' + credential(), Accept: 'application/json' } }, resolve);
        request.once('error', reject);
      });
      if (response.statusCode !== 200 || !response.headers['content-type']?.startsWith('application/json')) throw new Error('Model directory unavailable');
      let raw = '', bytes = 0;
      for await (const chunk of response) { bytes += chunk.length; if (bytes > 131072) throw new Error('Model directory too large'); raw += chunk.toString('utf8'); }
      return accountModels(JSON.parse(raw));
    } catch { throw new LlmError('Model directory unavailable', 'PROVIDER_ERROR'); }
    finally { response?.destroy(); request?.destroy(); }
  };
  const selectedModel = async (provider, model) => {
    if (provider !== 'platform-models') throw new LlmError('Model is not approved', 'MODEL_NOT_FOUND');
    const selected = (await directory()).find(item => item.id === model);
    if (!selected) throw new LlmError('Model is not approved', 'MODEL_NOT_FOUND');
    return selected;
  };
  class ExternalModelAdapter extends LlmAdapter {
    providerInfo() { return { id: 'platform-models', name: '平台模型网关' }; }
    providerRetryPolicy() { return { mode: 'normal', maxRetries: 0, retryableCodes: [], initialDelayMs: 1000, maxDelayMs: 1000, jitterRatio: 0 }; }
    async listModels() { return (await directory()).map(model => ({ provider: 'platform-models', id: model.id, name: model.name, inputModalities: ['text'] })); }
    async resolveModel(provider, model) {
      // Bootstrap resolves its frozen default before the network is ready. Actual
      // inference and directory reads always recheck current account authority.
      const selected = provider === 'platform-models' && model === config.model ? { ...config, name: config.response_models[0] } : await selectedModel(provider, model);
      return { provider, id: model, name: selected.name, inputModalities: ['text'], context: { contextWindow: selected.context_window }, defaultMaxTokens: Math.min(4096, selected.max_output_tokens) };
    }
    async *stream(options) {
      let response, request;
      try {
        const selected = await selectedModel(options.provider, options.model);
        const body = JSON.stringify(serializeMessages(options, selected));
        const key = credential();
        const signal = AbortSignal.any([...(options.signal ? [options.signal] : []), AbortSignal.timeout(120000)]);
        response = await new Promise((resolve, reject) => {
          request = https.request(config.endpoint + '/v1/messages?scope=provider-account', { method: 'POST', agent, signal, headers: { Authorization: 'Bearer ' + key, 'Content-Type': 'application/json', Accept: 'text/event-stream', 'anthropic-version': '2023-06-01' } }, resolve);
          request.setTimeout(30000, () => request.destroy(new Error('Model idle timeout')));
          request.once('error', reject); request.end(body);
        });
        if (response.statusCode !== 200) {
          const code = response.statusCode === 401 || response.statusCode === 403 ? 'AUTHENTICATION_FAILED' : response.statusCode === 429 ? 'RATE_LIMITED' : 'PROVIDER_ERROR';
          throw new LlmError('External model request rejected', code, { status: response.statusCode });
        }
        if (!response.headers['content-type']?.startsWith('text/event-stream')) throw new LlmError('External model stream unavailable', 'MALFORMED_RESPONSE');
        yield* translateMessages(sseEvents(response), selected);
      } catch (error) {
        if (error instanceof LlmError) throw error;
        throw new LlmError('External model request failed', ['UNSUPPORTED_OPTION', 'UNSUPPORTED_CONTENT', 'INVALID_REQUEST', 'MALFORMED_RESPONSE'].includes(error?.code) ? error.code : 'PROVIDER_ERROR');
      } finally { response?.destroy(); request?.destroy(); }
    }
  }
  ctx.llm.registerAdapter(['platform-models'], new ExternalModelAdapter());
}

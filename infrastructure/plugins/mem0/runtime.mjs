import { createHash } from 'node:crypto';

export class Mem0Error extends Error {}

export function userText(messages) {
  return (messages ?? [])
    .filter((message) => message?.role === 'user' && message.source?.kind === 'user')
    .map(messageText)
    .filter(Boolean)
    .join('\n\n');
}

export function messageText(message) {
  return Array.isArray(message?.content)
    ? message.content
        .filter((part) => part?.type === 'text' && typeof part.text === 'string')
        .map((part) => part.text)
        .join('\n')
        .trim()
    : '';
}

export class Mem0Runtime {
  constructor(config) {
    this.config = config;
    this.sessions = new Map();
    this.queue = Promise.resolve();
    this.disposed = false;
    this.controllers = new Set();
    this.pendingCount = 0;
    this.bufferedBytes = 0;
    this.stats = { saved: 0, failed: 0, dropped: 0, lastError: '' };
  }

  state(session) {
    if (!session?.id || session.header?.origin === 'subagent' || this.disposed) return null;
    if (!this.sessions.has(session.id)) {
      if (this.sessions.size >= 100) {
        this.stats.dropped++;
        this.stats.lastError = 'Session limit reached';
        return null;
      }
      this.sessions.set(session.id, { pending: [], seen: new Set(), lastRecallTurn: null });
    }
    return this.sessions.get(session.id);
  }

  async request(path, body, signal) {
    if (this.disposed) throw new Mem0Error('Mem0 plugin is disabled');
    const controller = new AbortController();
    this.controllers.add(controller);
    const timer = setTimeout(() => controller.abort(), this.config.timeoutMs);
    const abort = () => controller.abort();
    if (signal?.aborted) controller.abort();
    else signal?.addEventListener('abort', abort, { once: true });
    try {
      const response = await fetch(this.config.endpoint + path, {
        method: 'POST',
        redirect: 'error',
        signal: controller.signal,
        headers: { 'Content-Type': 'application/json', 'X-API-Key': this.config.apiKey },
        body: JSON.stringify(body),
      });
      if (!response.ok) {
        await response.body?.cancel();
        throw new Mem0Error('Mem0 request failed (HTTP ' + response.status + ')');
      }
      if (Number(response.headers.get('content-length')) > this.config.maxResponseBytes) {
        await response.body?.cancel();
        throw new Mem0Error('Mem0 response exceeded the size limit');
      }
      let size = 0;
      const parts = [];
      for await (const part of response.body ?? []) {
        size += part.byteLength;
        if (size > this.config.maxResponseBytes)
          throw new Mem0Error('Mem0 response exceeded the size limit');
        parts.push(part);
      }
      let result;
      try {
        result = JSON.parse(Buffer.concat(parts).toString('utf8'));
      } catch {
        throw new Mem0Error('Mem0 returned invalid JSON');
      }
      if (!result || !Array.isArray(result.results))
        throw new Mem0Error('Mem0 returned an invalid result');
      return result.results;
    } catch (error) {
      if (error instanceof Mem0Error) throw error;
      throw new Mem0Error(
        controller.signal.aborted
          ? 'Mem0 request cancelled or timed out'
          : 'Mem0 service unavailable',
      );
    } finally {
      clearTimeout(timer);
      signal?.removeEventListener('abort', abort);
      this.controllers.delete(controller);
    }
  }

  async search(query, topK = this.config.topK, signal) {
    if (typeof query !== 'string' || !query.trim() || query.length > this.config.maxQueryChars)
      throw new Mem0Error('Provide a non-empty query within the configured length limit');
    if (!Number.isInteger(topK) || topK < 1 || topK > 20)
      throw new Mem0Error('Search limit must be an integer from 1 to 20');
    const rows = await this.request(
      '/search',
      {
        query: query.trim(),
        filters: { user_id: this.config.userId, scope: 'personal' },
        top_k: topK,
      },
      signal,
    );
    return rows
      .slice(0, topK)
      .filter((row) => typeof row?.memory === 'string')
      .map((row) => ({
        id: String(row.id ?? ''),
        memory: row.memory.slice(0, this.config.maxContentChars),
        ...(Number.isFinite(row.score) ? { score: row.score } : {}),
      }));
  }

  async remember(content, metadata = {}, signal) {
    if (
      typeof content !== 'string' ||
      !content.trim() ||
      content.length > this.config.maxContentChars
    )
      throw new Mem0Error('Provide non-empty content within the configured length limit');
    try {
      const rows = await this.request(
        '/memories',
        {
          messages: [{ role: 'user', content: content.trim() }],
          user_id: this.config.userId,
          agent_id: createHash('sha256')
            .update('dsh-mem0:' + this.config.userId)
            .digest('hex'),
          infer: this.config.infer,
          metadata: { ...metadata, scope: 'personal', plugin: '@harness-cloud/dsh-mem0-memory' },
        },
        signal,
      );
      const ids = rows
        .filter((row) => typeof row?.id === 'string' && row.id.length > 0)
        .map((row) => row.id);
      if (!ids.length) throw new Mem0Error('Mem0 did not return a save receipt');
      this.stats.saved++;
      return { saved: true, ids };
    } catch (error) {
      this.stats.failed++;
      this.stats.lastError =
        'Save failed; its outcome may be unknown. Check Mem0 before saving again.';
      throw new Mem0Error(this.stats.lastError + ' ' + error.message);
    }
  }

  capture(session, event) {
    if (!this.config.autoCapture) return;
    const state = this.state(session);
    if (!state) return;
    const message =
      event?.type === 'user/message'
        ? event.data
        : this.config.captureAssistant && event?.type === 'assistant/message'
          ? event.data?.message
          : null;
    if (
      !message ||
      (message.role === 'user'
        ? message.source?.kind !== 'user'
        : message.role !== 'assistant' || message.source?.kind !== 'model')
    )
      return;
    const text = messageText(message);
    if (!text) return;
    const identity = String(message.id ?? event.seq ?? '');
    if (!identity) {
      this.stats.dropped++;
      this.stats.lastError = 'Capture skipped an event without a stable identity';
      return;
    }
    if (state.seen.has(identity)) return;
    const bytes = Buffer.byteLength(text) + 20;
    if (
      state.seen.size >= 2000 ||
      this.pendingCount >= 100 ||
      this.bufferedBytes + bytes > 1048576 ||
      text.length > this.config.maxContentChars - 20
    ) {
      this.stats.dropped++;
      this.stats.lastError = 'Capture limit reached; some messages were not saved';
      return;
    }
    state.seen.add(identity);
    this.pendingCount++;
    this.bufferedBytes += bytes;
    state.pending.push({
      content: (message.role === 'assistant' ? 'Assistant message:\n' : '') + text,
      session_id: session.id,
      message_id: identity,
      role: message.role,
      bytes,
    });
  }

  flush(session) {
    const state = this.sessions.get(session?.id);
    const pending = state?.pending.splice(0) ?? [];
    const task = this.queue.then(async () => {
      let failure;
      for (const item of pending) {
        try {
          await this.remember(item.content, {
            session_id: item.session_id,
            message_id: item.message_id,
            role: item.role,
            source: 'dsh-auto-capture',
          });
        } catch (error) {
          failure = error;
        } finally {
          this.pendingCount--;
          this.bufferedBytes -= item.bytes;
        }
      }
      if (failure) throw failure;
    });
    // POST outcomes can be unknown; removed entries are never automatically retried.
    this.queue = task.catch(() => {});
    return task;
  }

  status() {
    return {
      autoRecall: this.config.autoRecall,
      autoCapture: this.config.autoCapture,
      captureAssistant: this.config.captureAssistant,
      infer: this.config.infer,
      ...this.stats,
      pending: this.pendingCount,
    };
  }

  async dispose() {
    this.disposed = true;
    for (const controller of this.controllers) controller.abort();
    for (const state of this.sessions.values()) {
      for (const item of state.pending.splice(0)) {
        this.stats.dropped++;
        this.pendingCount--;
        this.bufferedBytes -= item.bytes;
      }
    }
    await this.queue;
    this.sessions.clear();
  }
}

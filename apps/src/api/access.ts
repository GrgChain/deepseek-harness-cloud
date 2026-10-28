import type { CurrentUser } from './identity';
import type { Instance } from './resources';
import { APIError } from './error';
import { showWorkspaceTransition } from './workspace-transition';

interface Challenge { challenge_id: string; expires_at: string }
interface Handoff extends Challenge { handoff_url: string; ticket: string }
const uuid = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/;

export function accessOrigin(instance: Instance, portalOrigin: string): string {
 const portal = new URL(portalOrigin);
 const expected = `i-${instance.id.replaceAll('-', '')}.${portal.hostname.slice('portal.'.length)}`;
 if (!uuid.test(instance.id) || portal.protocol !== 'https:' || !portal.hostname.startsWith('portal.') || portal.username || portal.password || portal.search || portal.hash || portal.pathname !== '/' || instance.access_host !== expected) throw new Error('工作空间访问地址无效，请刷新实例状态');
 return `https://${expected}${portal.port ? ':' + portal.port : ''}`;
}

class WorkspaceConnectionError extends Error {}

export class AccessClient {
 constructor(private readonly fetcher: typeof fetch = (...args) => fetch(...args)) {}
 private async response<T>(path: string, init: RequestInit): Promise<T> {
  const response = await this.fetcher(path, { ...init, cache: 'no-store', redirect: 'error', signal: AbortSignal.timeout(5000) });
  if (!response.ok) { const problem: { code?: unknown } = await response.json().catch(() => ({})); throw new APIError(response.status, typeof problem.code === 'string' ? problem.code : 'DEPENDENCY_UNAVAILABLE'); }
  return response.json() as Promise<T>;
 }
 async prepare(user: CurrentUser, instance: Instance, portalOrigin: string): Promise<Handoff> {
  if (instance.owner_id !== user.id || !instance.can_enter) throw new Error('该工作空间暂不可进入，请刷新实例状态');
  const origin = accessOrigin(instance, portalOrigin);
  let challenge: Challenge;
  try { challenge = await this.response<Challenge>(origin + '/__dsh/challenge', { credentials: 'include' }); }
  catch (error) {
   // Browser fetch cannot distinguish TLS trust, DNS, CORS and network failures.
   // Only instance-site transport failures need the top-level connection recovery.
   if (error instanceof TypeError || error instanceof DOMException && error.name === 'TimeoutError') throw new WorkspaceConnectionError('暂时无法连接工作空间，请检查网络后重试');
   throw error;
  }
  if (!uuid.test(challenge.challenge_id) || !Number.isFinite(Date.parse(challenge.expires_at)) || Date.parse(challenge.expires_at) <= Date.now() + 5000 || Date.parse(challenge.expires_at) > Date.now() + 65_000) throw new Error('访问挑战已失效，请重新进入');
  const handoff = await this.response<Handoff>(`/v1/instances/${instance.id}:access`, { method: 'POST', credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': user.csrf_token }, body: JSON.stringify({ challenge_id: challenge.challenge_id }) });
  if (handoff.handoff_url !== origin + '/__dsh/handoff' || handoff.challenge_id !== challenge.challenge_id || !/^[A-Za-z0-9_-]{43}$/.test(handoff.ticket) || !Number.isFinite(Date.parse(handoff.expires_at)) || Date.parse(handoff.expires_at) <= Date.now() || Date.parse(handoff.expires_at) > Math.min(Date.parse(challenge.expires_at), Date.now() + 45_000)) throw new Error('访问交接响应无效，请重新进入');
  return handoff;
 }
 async prepareReady(user: CurrentUser, instance: Instance, portalOrigin: string, cancelled: () => boolean, onWaiting: () => void): Promise<Handoff> {
  const deadline = Date.now() + 60_000;
  let current = instance;
  let checks = 0;
  for (;;) {
   if (cancelled()) throw new Error('工作空间连接已取消');
   try {
    if (current.can_enter) return await this.prepare(user, current, portalOrigin);
   } catch (error) {
    if (!(error instanceof APIError) || error.status !== 409 || error.code !== 'STATE_CONFLICT') throw error;
   }
   if (current.owner_id !== user.id || current.id !== instance.id || current.observed_state !== 'RUNNING' || current.desired_state !== 'RUNNING' || current.active_operation_id) throw new Error('该工作空间状态已变化，请返回实例详情查看');
   accessOrigin(current, portalOrigin);
   onWaiting();
   if (Date.now() >= deadline) throw new Error('工作空间仍未就绪，请稍后重试或查看运行检查');
   const delay = [0, 250, 500, 1000][checks++] ?? 2000;
   if (delay) await new Promise(resolve => setTimeout(resolve, Math.min(delay, deadline - Date.now())));
   if (cancelled()) throw new Error('工作空间连接已取消');
   if (Date.now() >= deadline) throw new Error('工作空间仍未就绪，请稍后重试或查看运行检查');
   const refreshed = await this.response<Instance>(`/v1/instances/${instance.id}`, { credentials: 'same-origin' });
   if (!refreshed || refreshed.id !== instance.id || refreshed.owner_id !== user.id || refreshed.observed_state !== 'RUNNING' || refreshed.desired_state !== 'RUNNING' || refreshed.active_operation_id || typeof refreshed.can_enter !== 'boolean' || !Array.isArray(refreshed.allowed_actions)) throw new Error('该工作空间状态已变化，请返回实例详情查看');
   accessOrigin(refreshed, portalOrigin);
   if (refreshed.can_enter && !refreshed.allowed_actions.includes('access')) throw new APIError(403, 'FORBIDDEN');
   current = refreshed;
  }
 }
}

// Continuation contains no authorization material and is scoped to this popup,
// current user, instance and Portal. Only the original click can create it.
const resumeKey = 'hc:workspace-connect:1';
const pending = new Set<string>();
const client = new AccessClient();
function submitHandoff(target: Window, handoff: Handoff) {
 if (target.closed) throw new Error('工作空间窗口已关闭，请重新进入');
 const form = target.document.createElement('form'); form.method = 'POST'; form.action = handoff.handoff_url;
 for (const [name, value] of Object.entries({ ticket: handoff.ticket, challenge_id: handoff.challenge_id })) { const input = target.document.createElement('input'); input.type = 'hidden'; input.name = name; input.value = value; form.append(input); }
 target.document.body.append(form); form.submit(); form.remove();
}
function entryError(error: unknown): Error {
 if (error instanceof DOMException && error.name === 'TimeoutError') return new Error('工作空间连接超时，请稍后重试');
 if (error instanceof APIError || error instanceof WorkspaceConnectionError || error instanceof Error && /^(访问|工作空间|该工作空间)/.test(error.message)) return error;
 return new Error('连接工作空间失败，请检查网络或平台状态后重试');
}
// Called only on the fixed Portal return route. Consume before requesting any
// fresh challenge; reloads and failed retries cannot create a redirect loop.
export async function resumeWorkspace(user: CurrentUser, instance: Instance): Promise<boolean> {
 let intent: { user?: string; instance?: string; origin?: string; expires?: number } | null = null;
 try { const saved = window.sessionStorage.getItem(resumeKey); window.sessionStorage.removeItem(resumeKey); if (saved) intent = JSON.parse(saved); } catch { return false; }
 if (!intent || intent.user !== user.id || intent.instance !== instance.id || intent.origin !== window.location.origin || typeof intent.expires !== 'number' || intent.expires <= Date.now() || intent.expires > Date.now() + 120_000) return false;
 try { submitHandoff(window, await client.prepareReady(user, instance, window.location.origin, () => false, () => {})); return true; }
 catch (error) { throw entryError(error); }
}
// Open synchronously from the click. Tickets remain in memory/hidden POST
// fields; no credentials appear in URLs, query caches or continuation storage.
export async function enterWorkspace(user: CurrentUser, instance: Instance) {
 if (pending.has(instance.id)) throw new Error('该工作空间正在连接，请稍候');
 const popup = window.open('about:blank', '_blank');
 if (!popup) throw new Error('浏览器阻止了新窗口，请允许此网站弹出窗口后点击“进入工作空间”');
 popup.opener = null;
 let cancelled = false;
 showWorkspaceTransition(popup, instance, window.location.origin, undefined, () => { cancelled = true; });
 pending.add(instance.id);
 try {
  const handoff = await client.prepareReady(user, instance, window.location.origin, () => cancelled || popup.closed, () => {
   if (!popup.closed) {
    const description = popup.document.getElementById('entry-description');
    if (description) description.textContent = '工作空间正在完成启动，准备好后会自动进入。';
   }
  });
  if (!cancelled) {
   const description=popup.document.getElementById('entry-description');
   if(description)description.textContent='连接已就绪，正在打开工作空间。';
   try { popup.sessionStorage.setItem(resumeKey, JSON.stringify({user:user.id,instance:instance.id,origin:window.location.origin,expires:Date.now()+120_000})); } catch { /* The details page remains available for manual retry. */ }
   submitHandoff(popup, handoff);
  }
 } catch (error) {
  if (cancelled || popup.closed) return;
  if (error instanceof WorkspaceConnectionError && !popup.closed) {
   try {
    popup.sessionStorage.setItem(resumeKey, JSON.stringify({user:user.id,instance:instance.id,origin:window.location.origin,expires:Date.now()+120_000}));
    popup.location.replace(accessOrigin(instance, window.location.origin) + '/__dsh/connect');
    return;
   } catch { popup.close(); throw new Error('无法自动继续连接，请允许会话存储后重试'); }
  }
  const failure = entryError(error);
  if (!popup.closed) showWorkspaceTransition(popup, instance, window.location.origin, failure);
  throw failure;
 } finally { pending.delete(instance.id); }
}

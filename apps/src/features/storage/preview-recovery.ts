import type { PreviewFile } from './file-preview';

const RESUME_KEY = 'storage-preview-resume';
const RECOVERY_KEY = 'storage-preview-last-refresh';
const RESUME_TTL_MS = 2 * 60_000;

export function readResumedPreview(assetId: string): PreviewFile | null {
 try {
  const value = JSON.parse(sessionStorage.getItem(RESUME_KEY) ?? 'null');
  const file = value?.file;
  if (value?.assetId !== assetId || value.route !== location.pathname || Date.now() - value.time > RESUME_TTL_MS ||
   !file || !['workspace', 'dsh'].includes(file.root) || typeof file.path !== 'string' || typeof file.name !== 'string' ||
   !file.path || !file.name || file.path.split('/').some((part: string) => !part || part === '.' || part === '..')) return null;
  return { root: file.root, path: file.path, name: file.name };
 } catch { return null; }
}

export function clearResumedPreview() {
 try { sessionStorage.removeItem(RESUME_KEY); } catch { /* Storage may be unavailable. */ }
}

export function reloadFilePreview(assetId: string, file: PreviewFile) {
 try {
  sessionStorage.setItem(RESUME_KEY, JSON.stringify({ assetId, file, route: location.pathname, time: Date.now() }));
  sessionStorage.setItem(RECOVERY_KEY, JSON.stringify({ route: location.pathname, time: Date.now() }));
 } catch { /* Refresh still recovers a cached module failure. */ }
 location.reload();
}

// Reload only when the current HTML points to a different build. A normal network
// failure stays in the modal, without risking a refresh loop while offline.
export async function recoverOutdatedPreview(assetId: string, file: PreviewFile, signal: AbortSignal): Promise<boolean> {
 try {
  const response = await fetch(location.pathname, { credentials: 'same-origin', cache: 'no-store', signal: AbortSignal.any([signal, AbortSignal.timeout(5000)]) });
  if (!response.ok) return false;
  const html = new DOMParser().parseFromString(await response.text(), 'text/html');
  const current = document.querySelector<HTMLScriptElement>('script[type="module"][src]')?.getAttribute('src');
  const next = html.querySelector<HTMLScriptElement>('script[type="module"][src]')?.getAttribute('src');
  let recentlyRefreshed = false;
  try {
   const previous = JSON.parse(sessionStorage.getItem(RECOVERY_KEY) ?? 'null');
   recentlyRefreshed = previous?.route === location.pathname && Date.now() - previous.time < RESUME_TTL_MS;
  } catch { return false; }
  // Do not automatically refresh a second time if recovery itself failed.
  if (!current || !next || new URL(current, location.href).href === new URL(next, location.href).href || signal.aborted || recentlyRefreshed) return false;
  reloadFilePreview(assetId, file);
  return true;
 } catch { return false; }
}

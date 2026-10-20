import { open, lstat, rename, unlink } from 'node:fs/promises';
import { constants } from 'node:fs';
import { dirname } from 'node:path';
import { randomUUID } from 'node:crypto';
export async function readPrivate(path) {
  const file = await open(path, constants.O_RDONLY | constants.O_NOFOLLOW);
  try { const info = await file.stat(); if (!info.isFile() || (info.mode & 0o077) !== 0 || info.size > 65536) throw new Error('Private file permissions or size are invalid'); return await file.readFile('utf8'); } finally { await file.close(); }
}
export async function writePrivate(path, value) {
  const temp = `${path}.${randomUUID()}.tmp`;
  const file = await open(temp, 'wx', 0o600);
  try { await file.writeFile(JSON.stringify(value)); await file.sync(); } finally { await file.close(); }
  try { await rename(temp, path); const parent = await open(dirname(path), 'r'); try { await parent.sync(); } finally { await parent.close(); } } catch (error) { await unlink(temp).catch(() => {}); throw error; }
}
export async function privateDirectory(path) { const info = await lstat(path); if (!info.isDirectory() || info.isSymbolicLink() || (info.mode & 0o077) !== 0) throw new Error('Runtime channel directory must be private'); }

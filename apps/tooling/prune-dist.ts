import { existsSync, readFileSync, readdirSync, unlinkSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';
import type { Plugin } from 'vite';

interface BuildAssetHistory {
  current: string[];
  previous: string[];
}

const historyFilename = '.build-assets.json';

function readHistory(outDir: string): BuildAssetHistory {
  const path = join(outDir, historyFilename);
  if (!existsSync(path)) return { current: [], previous: [] };
  try {
    const parsed = JSON.parse(readFileSync(path, 'utf8')) as Partial<BuildAssetHistory>;
    return {
      current: Array.isArray(parsed.current)
        ? parsed.current.filter((item): item is string => typeof item === 'string')
        : [],
      previous: Array.isArray(parsed.previous)
        ? parsed.previous.filter((item): item is string => typeof item === 'string')
        : [],
    };
  } catch {
    return { current: [], previous: [] };
  }
}

export function pruneStaleAssets(outDir: string, emittedFileNames: string[]): number {
  const assetsDir = join(outDir, 'assets');
  if (!existsSync(assetsDir)) return 0;
  const current = emittedFileNames
    .filter((name) => name.startsWith('assets/'))
    .map((name) => name.slice('assets/'.length))
    .filter((name) => name.length > 0 && !name.includes('/'));
  const prior = readHistory(outDir);
  const keep = new Set([...current, ...prior.current]);
  let removed = 0;
  for (const name of readdirSync(assetsDir)) {
    if (keep.has(name)) continue;
    unlinkSync(join(assetsDir, name));
    removed += 1;
  }
  const next: BuildAssetHistory = { current, previous: prior.current };
  writeFileSync(join(outDir, historyFilename), JSON.stringify(next, null, 2) + '\n');
  return removed;
}

// Keep one prior build so already-open tabs can still load lazy chunks,
// while preventing dist/assets from growing without bound across builds.
export function pruneDistAssets(): Plugin {
  return {
    name: 'prune-dist-assets',
    apply: 'build',
    writeBundle(options, bundle) {
      if (!options.dir) return;
      pruneStaleAssets(options.dir, Object.keys(bundle));
    },
  };
}

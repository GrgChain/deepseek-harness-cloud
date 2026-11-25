import { mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { pruneStaleAssets } from './prune-dist';

const dirs: string[] = [];

afterEach(() => {
  while (dirs.length) rmSync(dirs.pop()!, { recursive: true, force: true });
});

describe('pruneStaleAssets', () => {
  it('retains current and immediately previous build assets while deleting older ones', () => {
    const outDir = mkdtempSync(join(tmpdir(), 'hc-prune-dist-'));
    dirs.push(outDir);
    const assetsDir = join(outDir, 'assets');
    mkdirSync(assetsDir, { recursive: true });

    writeFileSync(join(assetsDir, 'index-v1.js'), 'v1');
    writeFileSync(join(assetsDir, 'orphan-v0.js'), 'v0');
    expect(pruneStaleAssets(outDir, ['assets/index-v1.js', 'index.html'])).toBe(1);
    expect(readdirSync(assetsDir).sort()).toEqual(['index-v1.js']);

    writeFileSync(join(assetsDir, 'index-v2.js'), 'v2');
    expect(pruneStaleAssets(outDir, ['assets/index-v2.js'])).toBe(0);
    expect(readdirSync(assetsDir).sort()).toEqual(['index-v1.js', 'index-v2.js']);

    writeFileSync(join(assetsDir, 'index-v3.js'), 'v3');
    expect(pruneStaleAssets(outDir, ['assets/index-v3.js'])).toBe(1);
    expect(readdirSync(assetsDir).sort()).toEqual(['index-v2.js', 'index-v3.js']);
  });
});

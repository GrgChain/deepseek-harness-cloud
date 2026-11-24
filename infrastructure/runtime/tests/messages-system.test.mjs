import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { serializeMessages } from '../messages-wire.mjs';
import { readPrivate } from '../private-file.mjs';

test('serializeMessages preserves options.system and concatenates multiple system messages', () => {
  const payload = serializeMessages(
    {
      model: 'test-model',
      system: 'base system',
      messages: [
        { role: 'system', content: [{ type: 'text', text: 'extra system 1' }] },
        { role: 'system', content: [{ type: 'text', text: 'extra system 2' }] },
        { role: 'user', content: [{ type: 'text', text: 'hello' }] },
      ],
    },
    { model: 'test-model', max_output_tokens: 4096 },
  );
  assert.equal(payload.system, 'base system\n\nextra system 1\n\nextra system 2');
});

test('readPrivate accepts valid configs larger than 64 KiB up to 256 KiB', async () => {
  const dir = await mkdtemp(join(tmpdir(), 'hc-priv-'));
  try {
    const file = join(dir, 'config.json');
    const large = 'x'.repeat(90 * 1024);
    await writeFile(file, large, { mode: 0o600 });
    const content = await readPrivate(file);
    assert.equal(content.length, large.length);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

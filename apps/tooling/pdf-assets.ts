import { readFileSync, readdirSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import type { Plugin } from 'vite';

// Keep fonts, CMaps, and decoders on the same origin in development and production.
export function pdfAssets(): Plugin {
 const directory = new URL('../node_modules/pdfjs-dist/', import.meta.url);
 const { version } = JSON.parse(readFileSync(new URL('package.json', directory), 'utf8')) as { version: string };
 const assets = ['cmaps', 'standard_fonts', 'wasm', 'iccs'].flatMap(folder =>
  readdirSync(new URL(`${folder}/`, directory)).map(name => ({
   path: `pdfjs/${version}/${folder}/${name}`,
   source: readFileSync(fileURLToPath(new URL(`${folder}/${name}`, directory))),
  })),
 );
 return {
  name: 'local-pdf-assets',
  configureServer(server) {
   const files = new Map(assets.map(asset => [`/${asset.path}`, asset.source]));
   server.middlewares.use((request, response, next) => {
    const path = request.url?.split('?')[0] ?? '';
    const source = files.get(path);
    if (!source) return next();
    response.setHeader('Content-Type', path.endsWith('.wasm') ? 'application/wasm' : path.endsWith('.js') ? 'text/javascript' : 'application/octet-stream');
    response.setHeader('Cache-Control', 'public, max-age=31536000, immutable');
    response.end(source);
   });
  },
  generateBundle() {
   for (const asset of assets) this.emitFile({ type: 'asset', fileName: asset.path, source: asset.source });
  },
 };
}

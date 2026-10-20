import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { existsSync,readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { localAccess } from './tooling/local-access';
import { pdfAssets } from './tooling/pdf-assets';

const authDirectory=fileURLToPath(new URL('../.local/auth/',import.meta.url));
const accessDirectory=fileURLToPath(new URL('../.local/access/',import.meta.url));
const accessEnabled=existsSync(fileURLToPath(new URL('../.local/access-config.json',import.meta.url)));
const tlsDirectory=accessEnabled ? accessDirectory : authDirectory;
const tls=existsSync(tlsDirectory+'cert.pem') ? { cert:readFileSync(tlsDirectory+'cert.pem'),key:readFileSync(tlsDirectory+'key.pem') } : undefined;

export default defineConfig({
  plugins: [react(), pdfAssets(), ...(accessEnabled ? [localAccess()] : [])],
  worker: { format: 'es' },
  // Open tabs may still reference lazy modules from the previous build.
  build: { emptyOutDir: false },
  preview: { port: 42013, strictPort: true, host: '127.0.0.1' },
  server: {
    https: tls,
    port: 42013,
    strictPort: true,
    proxy: Object.fromEntries(['/v1', '/auth', '/oidc', '/livez', '/readyz'].map(path => [path, 'http://127.0.0.1:18080'])),
  },
});

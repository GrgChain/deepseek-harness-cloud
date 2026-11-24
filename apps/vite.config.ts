import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { existsSync, readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { localAccess } from './tooling/local-access';
import { pdfAssets } from './tooling/pdf-assets';
import { pruneDistAssets } from './tooling/prune-dist';

const authDirectory = fileURLToPath(new URL('../.local/auth/', import.meta.url));
const accessDirectory = fileURLToPath(new URL('../.local/access/', import.meta.url));
const accessConfigPath = fileURLToPath(new URL('../.local/access-config.json', import.meta.url));
const accessEnabled = existsSync(accessConfigPath);
const ipOnly = accessEnabled && JSON.parse(readFileSync(accessConfigPath, 'utf8')).ip_only === true;
const tlsDirectory = accessEnabled ? accessDirectory : authDirectory;
const tls = existsSync(tlsDirectory + 'cert.pem')
  ? { cert: readFileSync(tlsDirectory + 'cert.pem'), key: readFileSync(tlsDirectory + 'key.pem') }
  : undefined;

export default defineConfig({
  plugins: [
    react(),
    pdfAssets(),
    pruneDistAssets(),
    ...(accessEnabled ? [localAccess({ ipOnly })] : []),
  ],
  worker: { format: 'es' },
  // Open tabs may still reference lazy modules from the previous build;
  // pruneDistAssets retains the current and previous build while removing older files.
  build: {
    emptyOutDir: false,
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) return undefined;
          if (
            /\/node_modules\/(react|react-dom|react-router|react-router-dom|scheduler|@tanstack\/react-query)\//.test(
              id,
            )
          )
            return 'vendor-react';
          if (/\/node_modules\/(antd|@ant-design\/|rc-|@rc-component\/)/.test(id))
            return 'vendor-antd';
          if (/\/node_modules\/lucide-react\//.test(id)) return 'vendor-icons';
          if (
            /\/node_modules\/(react-markdown|remark-|rehype-|mdast-|hast-|micromark|unist-|vfile)/.test(
              id,
            )
          )
            return 'vendor-markdown';
          return undefined;
        },
      },
    },
  },
  preview: { port: 42013, strictPort: true, host: '127.0.0.1' },
  server: {
    https: tls,
    port: 42013,
    strictPort: true,
    proxy: Object.fromEntries(
      ['/v1', '/auth', '/oidc', '/livez', '/readyz'].map((path) => [
        path,
        'http://127.0.0.1:18080',
      ]),
    ),
  },
});

import { defineConfig, loadEnv } from 'vite';
import { fileURLToPath } from 'node:url';

const entry = (name: string) => fileURLToPath(new URL(`./${name}.html`, import.meta.url));

export default defineConfig(({ command, mode }) => {
  const env = loadEnv(mode, process.cwd(), ['VITE_', 'DEV_API_']);
  if (command === 'build' && env.VITE_ENABLE_MOCK && env.VITE_ENABLE_MOCK !== 'false') {
    throw new Error('Mock services are forbidden in production builds');
  }
  const target = new URL(env.DEV_API_ORIGIN ?? 'http://127.0.0.1:8080');
  if (target.protocol !== 'http:' || !['127.0.0.1','[::1]','localhost'].includes(target.hostname) || target.username || target.password) {
    throw new Error('DEV_API_ORIGIN must be an HTTP loopback origin');
  }
  return {
    appType: 'mpa',
    server: { host: '127.0.0.1', proxy: { '/api/v1/': { target: target.origin, changeOrigin: false } } },
    preview: { host: '127.0.0.1' },
    build: { rollupOptions: { input: { user: entry('user'), rider: entry('rider'), admin: entry('admin') } } },
  };
});

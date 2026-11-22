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
    server: {
      host: '127.0.0.1',
      proxy: { '/api/v1/': { target: target.origin, changeOrigin: false } },
      // Windows 上编辑器/工具原子保存会在源文件旁生成 .xxx.tmpdir/ 与 .tmp 临时文件，
      // 文件被占用时 chokidar 抛 EBUSY 会直接带崩 dev server，这里忽略这些临时路径。
      watch: { ignored: ['**/*.tmpdir/**', '**/.entry.ts.*/**', '**/*.tmp'] },
    },
    preview: { host: '127.0.0.1' },
    build: mode === 'merchant'
      ? { outDir: 'dist-merchant', rollupOptions: { input: { merchant: entry('merchant') } } }
      : { rollupOptions: { input: { user: entry('user'), rider: entry('rider'), admin: entry('admin') } } },
  };
});

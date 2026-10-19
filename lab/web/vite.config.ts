import { fileURLToPath } from 'node:url';

import react from '@vitejs/plugin-react';
import { defineConfig } from 'vitest/config';

// The Go panel embeds the build from here; the Docker image builds it in its
// own stage.
const outDir = fileURLToPath(new URL('../internal/dashboard/web/app', import.meta.url));

// The lab's own port, for `npm run dev` against a running lab.
const labURL = 'http://localhost:8090';

export default defineConfig({
  plugins: [react()],
  build: { outDir, emptyOutDir: true, sourcemap: false },
  server: { proxy: { '/api': labURL, '/rounds': labURL } },
  test: { environment: 'jsdom', setupFiles: ['src/test/setup.ts'], restoreMocks: true },
});

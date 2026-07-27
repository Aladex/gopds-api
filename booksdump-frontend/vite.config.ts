// defineConfig comes from vitest/config rather than vite so the `test` block is
// typed; it is the same Vite config otherwise.
import path from 'node:path';

import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
    plugins: [react(), tailwindcss()],
    resolve: {
        alias: {
            '@': path.resolve(import.meta.dirname, 'src'),
        },
    },
    build: {
        // The Go binary embeds booksdump-frontend/build via go:embed, and
        // cmd/filesystem.go, cmd/middleware.go and cmd/routes.go all resolve
        // assets under that path. Keep Vite writing there rather than to its
        // default dist/.
        outDir: 'build',
        // create-react-app shipped source maps for production builds.
        sourcemap: true,
    },
    server: {
        // Same port create-react-app used, so existing bookmarks and the
        // documented development flow keep working.
        port: 3000,
    },
    test: {
        environment: 'jsdom',
        globals: true,
        setupFiles: 'src/setupTests.ts',
        css: false,
    },
});

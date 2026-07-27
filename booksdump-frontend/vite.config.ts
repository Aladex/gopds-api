// defineConfig comes from vitest/config rather than vite so the `test` block is
// typed; it is the same Vite config otherwise.
import path from 'node:path';

import { defineConfig } from 'vitest/config';
import react, { reactCompilerPreset } from '@vitejs/plugin-react';
import babel from '@rolldown/plugin-babel';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
    plugins: [
        react(),
        /*
         * The React Compiler memoises components and hooks automatically, and
         * gets the dependencies right, which hand-placed useMemo and useCallback
         * routinely do not.
         *
         * It only compiles what it can prove safe: anything that breaks the
         * rules of React is left exactly as written. The same analysis powers
         * the react-hooks lint rules, so whatever it skips is already reported
         * by `make lint-frontend`.
         *
         * The plugin itself runs on oxc; the compiler is a Babel pass, hence
         * the extra Babel step here.
         */
        babel({ presets: [reactCompilerPreset()] }),
        tailwindcss(),
    ],
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

import { readFileSync, readdirSync } from 'fs';
import { join } from 'path';

// Round 2 (review coverage): the provider must be the only place that opens
// a socket. A page-level `new WebSocket` (the pre-task pattern this work
// removed) cannot slip back in unnoticed.

const SRC = join(__dirname, '..', '..', '..');

const tsFiles = (dir: string): string[] =>
    readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
        const path = join(dir, entry.name);
        if (entry.isDirectory()) {
            return entry.name === '__tests__' || entry.name === 'node_modules' ? [] : tsFiles(path);
        }
        return /\.(ts|tsx)$/.test(entry.name) ? [path] : [];
    });

it('no page or hook opens its own WebSocket', () => {
    const offenders = tsFiles(SRC)
        .filter((path) => !path.endsWith('WebSocketContext.tsx'))
        .filter((path) => readFileSync(path, 'utf8').includes('new WebSocket('));
    expect(offenders).toEqual([]);
});

it('every event-driven admin view rides the shared socket', () => {
    for (const page of ['BookScanning.tsx', 'Duplicates.tsx', 'GenreManagement.tsx']) {
        const source = readFileSync(join(SRC, 'features', 'admin', page), 'utf8');
        expect(source, page).toContain('useWebSocket');
        expect(source, page).not.toContain('/api/ws');
    }
});

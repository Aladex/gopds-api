import React from 'react';
import { fireEvent, render } from '@testing-library/react';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import KindleVideo, { KINDLE_ILLUSTRATION, PLYR_SPRITE } from '@/features/kindle/KindleVideo';

// The how-to video plays through Plyr. The video is made apart from the
// interface and may be missing, or missing in one language; neither may leave
// an empty player behind. Plyr itself is replaced here — jsdom plays nothing —
// by a stand-in that records how it was built and whether it was taken down.

const translation = { t: (key: string) => key, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const players: { media: HTMLVideoElement; options: Record<string, unknown>; destroyed: boolean }[] =
    [];
vi.mock('plyr', () => ({
    default: class {
        record: (typeof players)[number];
        constructor(media: HTMLVideoElement, options: Record<string, unknown>) {
            this.record = { media, options, destroyed: false };
            players.push(this.record);
        }
        destroy() {
            this.record.destroyed = true;
        }
    },
}));

beforeEach(() => {
    translation.i18n.language = 'ru';
    players.length = 0;
});

it('plays the video in the interface language, silent and inline, through Plyr', () => {
    const { container } = render(<KindleVideo />);
    const video = container.querySelector('video')!;

    expect(video.getAttribute('src')).toBe('/videos/kindle-setup-ru.mp4');
    expect(video.getAttribute('poster')).toBe('/videos/kindle-setup-ru.jpg');
    expect(video.muted).toBe(true);
    expect(video).toHaveAttribute('playsinline');
    expect(video.preload).toBe('metadata');
    expect(players).toHaveLength(1);
    expect(players[0].media).toBe(video);
});

it('takes the English video for an English interface', () => {
    translation.i18n.language = 'en';
    const { container } = render(<KindleVideo />);

    expect(container.querySelector('video')!.getAttribute('src')).toBe(
        '/videos/kindle-setup-en.mp4',
    );
});

// The content security policy allows nothing from cdn.plyr.io, where Plyr
// looks for its icons and its blank video by default.
it('asks Plyr for nothing off this site', () => {
    render(<KindleVideo />);
    const { options } = players[0];

    expect(options.iconUrl).toBe(PLYR_SPRITE);
    expect(String(options.iconUrl)).toMatch(/^\//);
    expect(options.blankVideo).toBe('');
    expect(JSON.stringify(options)).not.toMatch(/cdn\.plyr\.io/);
});

// Built from this file's own location: a literal new URL(…, import.meta.url)
// is taken by the bundler for an asset to serve, not a file to read.
const frontend = join(dirname(fileURLToPath(import.meta.url)), '../../../..');
const read = (relative: string) => readFileSync(join(frontend, relative));

it('serves the sprite of the Plyr version installed', () => {
    expect(PLYR_SPRITE).toBe('/vendor/plyr-3.8.4.svg');
    const local = read('public/vendor/plyr-3.8.4.svg');
    const installed = read('node_modules/plyr/dist/plyr.svg');
    const version = JSON.parse(read('node_modules/plyr/package.json').toString('utf8')).version;
    // A version bump must bring its own sprite along.
    expect(PLYR_SPRITE).toContain(version);
    expect(local.equals(installed)).toBe(true);
});

it('takes its accent from the site’s palette', () => {
    const { getByTestId } = render(<KindleVideo />);

    expect(getByTestId('kindle-video').style.getPropertyValue('--plyr-color-main')).toBe(
        'var(--primary)',
    );
});

it('destroys the player when it goes away', () => {
    const { container, unmount } = render(<KindleVideo />);
    expect(container.querySelector('video')).not.toBeNull();

    unmount();

    expect(players).toHaveLength(1);
    expect(players[0].destroyed).toBe(true);
});

it('gives way to the illustration when the video is missing, and takes the player down', () => {
    const { container } = render(<KindleVideo />);

    fireEvent.error(container.querySelector('video')!);

    expect(players[0].destroyed).toBe(true);
    expect(container.querySelector('video')).toBeNull();
    expect(container.querySelector('img')!.getAttribute('src')).toBe(KINDLE_ILLUSTRATION);
});

// Plyr ships its controls' names in English only; they come from the
// interface's own translations, and a change of language builds the player
// again with the new words.
it('names the player’s controls in the interface language', () => {
    render(<KindleVideo />);
    const labels = players[0].options.i18n as Record<string, string>;

    expect(labels.play).toBe('plyr.play');
    expect(labels.enterFullscreen).toBe('plyr.enterFullscreen');
    expect(labels.seekLabel).toBe('plyr.seekLabel');
});

it('builds the player again when the language changes', () => {
    const { rerender } = render(<KindleVideo />);
    expect(players).toHaveLength(1);

    translation.i18n.language = 'en';
    translation.t = (key: string) => `en:${key}`;
    rerender(<KindleVideo />);

    expect(players).toHaveLength(2);
    expect(players[0].destroyed).toBe(true);
    expect((players[1].options.i18n as Record<string, string>).play).toBe('en:plyr.play');
    translation.t = (key: string) => key;
});

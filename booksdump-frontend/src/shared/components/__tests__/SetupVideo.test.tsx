import React from 'react';
import { act, fireEvent, render } from '@testing-library/react';

import SetupVideo, { useFileExists } from '@/shared/components/SetupVideo';

// SetupVideo without an illustration is an optional video: one recorded after
// the feature ships. Until it is known to play nothing of it shows, and a
// missing file leaves nothing behind — no empty player, no stand-in picture.
// Kindle's video, with its illustration, is held by its own tests.

const translation = { t: (key: string) => key, i18n: { language: 'en' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

const players: { media: HTMLVideoElement; destroyed: boolean }[] = [];
vi.mock('plyr', () => ({
    default: class {
        record: (typeof players)[number];
        constructor(media: HTMLVideoElement) {
            this.record = { media, destroyed: false };
            players.push(this.record);
        }
        destroy() {
            this.record.destroyed = true;
        }
    },
}));

const files = (lang: string) => ({
    src: `/videos/demo-${lang}.mp4`,
    poster: `/videos/demo-${lang}.jpg`,
});

/** Image as the browser has it: jsdom loads none, so the outcome is said here. */
const imagesThat = (outcome: 'load' | 'error') => {
    class Probe {
        onload: (() => void) | null = null;
        onerror: (() => void) | null = null;
        set src(_url: string) {
            queueMicrotask(() => (outcome === 'load' ? this.onload : this.onerror)?.());
        }
    }
    vi.stubGlobal('Image', Probe);
};

beforeEach(() => {
    players.length = 0;
});

afterEach(() => {
    vi.unstubAllGlobals();
});

it('stays out of sight until the video is known to play', () => {
    const { container, getByTestId } = render(
        <SetupVideo files={files} label="demo" data-testid="demo-video" />,
    );
    const video = container.querySelector('video')!;

    expect(video.getAttribute('src')).toBe('/videos/demo-en.mp4');
    expect(getByTestId('demo-video')).not.toBeVisible();

    fireEvent(video, new Event('loadedmetadata'));

    expect(getByTestId('demo-video')).toBeVisible();
});

it('leaves nothing behind when the video is missing, and says so', () => {
    const onMissing = vi.fn();
    const { container } = render(<SetupVideo files={files} label="demo" onMissing={onMissing} />);

    fireEvent.error(container.querySelector('video')!);

    expect(container).toBeEmptyDOMElement();
    expect(players[0].destroyed).toBe(true);
    expect(onMissing).toHaveBeenCalledTimes(1);
});

it('plays without a poster rather than with a stand-in when the poster is missing', async () => {
    imagesThat('error');
    const { container } = render(<SetupVideo files={files} label="demo" />);
    await act(async () => {});

    const video = container.querySelector('video')!;
    expect(video.hasAttribute('poster')).toBe(false);
    expect(container.querySelector('img')).toBeNull();
});

describe('useFileExists', () => {
    const Probe: React.FC<{ url: string }> = ({ url }) => {
        const exists = useFileExists(url);
        return <span>{String(exists)}</span>;
    };

    it.each([
        ['load', 'true'],
        ['error', 'false'],
    ] as const)('answers after the file %ss', async (outcome, answer) => {
        imagesThat(outcome);
        const { container } = render(<Probe url="/videos/demo-en.jpg" />);
        expect(container.textContent).toBe('null');

        await act(async () => {});

        expect(container.textContent).toBe(answer);
    });
});

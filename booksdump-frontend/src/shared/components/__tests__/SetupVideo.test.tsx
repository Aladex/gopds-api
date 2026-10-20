import React from 'react';
import { act, fireEvent, render } from '@testing-library/react';

import FadingTabPanel from '@/shared/components/FadingTabPanel';
import SetupVideo, { useFileExists } from '@/shared/components/SetupVideo';
import { Tabs } from '@/shared/ui/tabs';

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

describe('stops when it can no longer be seen', () => {
    // jsdom plays nothing: a video here is "playing" until its pause() is
    // called, and play() is watched so that nothing starts it again.
    let pause: ReturnType<typeof vi.spyOn>;
    let play: ReturnType<typeof vi.spyOn>;
    let observed: {
        callback: IntersectionObserverCallback;
        target?: Element;
        disconnected: boolean;
    }[];

    beforeEach(() => {
        pause = vi.spyOn(HTMLMediaElement.prototype, 'pause').mockImplementation(function (
            this: HTMLMediaElement,
        ) {
            Object.defineProperty(this, 'paused', { configurable: true, get: () => true });
        });
        play = vi.spyOn(HTMLMediaElement.prototype, 'play').mockResolvedValue(undefined);
        observed = [];
        vi.stubGlobal(
            'IntersectionObserver',
            class {
                record: (typeof observed)[number];
                constructor(callback: IntersectionObserverCallback) {
                    this.record = { callback, disconnected: false };
                    observed.push(this.record);
                }
                observe(target: Element) {
                    this.record.target = target;
                }
                disconnect() {
                    this.record.disconnected = true;
                }
            },
        );
    });

    afterEach(() => {
        pause.mockRestore();
        play.mockRestore();
        Reflect.deleteProperty(document, 'visibilityState');
    });

    /** A video that loaded — its player is in sight — and is playing. */
    const playing = (container: HTMLElement) => {
        const video = container.querySelector('video')!;
        fireEvent(video, new Event('loadedmetadata'));
        expect(video.parentElement).toBeVisible();
        Object.defineProperty(video, 'paused', { configurable: true, get: () => false });
        return video;
    };

    /** The profile's tabs: the video in one FadingTabPanel, another beside it. */
    const InTabs: React.FC<{ tab: string }> = ({ tab }) => (
        <Tabs value={tab}>
            <FadingTabPanel value="telegram" active={tab === 'telegram'}>
                <SetupVideo files={files} label="demo" />
            </FadingTabPanel>
            <FadingTabPanel value="opds" active={tab === 'opds'}>
                OPDS
            </FadingTabPanel>
        </Tabs>
    );

    it('when its tab is switched away, though its panel stays mounted', async () => {
        const { container, rerender } = render(<InTabs tab="telegram" />);
        playing(container);

        rerender(<InTabs tab="opds" />);
        await act(async () => {});

        expect(container.querySelector('video')).not.toBeNull();
        expect(pause).toHaveBeenCalledTimes(1);
    });

    it('when the outer tab holding its tabs is switched away', async () => {
        const Outer: React.FC<{ tab: string }> = ({ tab }) => (
            <Tabs value={tab}>
                <FadingTabPanel value="connections" active={tab === 'connections'}>
                    <InTabs tab="telegram" />
                </FadingTabPanel>
                <FadingTabPanel value="account" active={tab === 'account'}>
                    account
                </FadingTabPanel>
            </Tabs>
        );
        const { container, rerender } = render(<Outer tab="connections" />);
        playing(container);

        rerender(<Outer tab="account" />);
        await act(async () => {});

        expect(pause).toHaveBeenCalledTimes(1);
    });

    it.each([
        ['goes inert', (el: HTMLElement) => el.setAttribute('inert', '')],
        ['goes hidden', (el: HTMLElement) => el.setAttribute('hidden', '')],
        [
            'is a tab panel turned inactive',
            (el: HTMLElement) => {
                el.setAttribute('role', 'tabpanel');
                el.setAttribute('data-state', 'inactive');
            },
        ],
        [
            'is a dialog closing',
            (el: HTMLElement) => {
                el.setAttribute('role', 'dialog');
                el.setAttribute('data-state', 'closed');
            },
        ],
    ])('when what holds it %s', async (_name, conceal) => {
        const { container, getByTestId } = render(
            <div data-testid="holder">
                <SetupVideo files={files} label="demo" />
            </div>,
        );
        playing(container);

        conceal(getByTestId('holder'));
        await act(async () => {});

        expect(pause).toHaveBeenCalledTimes(1);
    });

    it('when the sheet or dialog holding it goes away', () => {
        const { container, unmount } = render(<SetupVideo files={files} label="demo" />);
        playing(container);

        unmount();

        expect(pause).toHaveBeenCalledTimes(1);
    });

    it('when the browser tab is hidden, and not when it is shown', () => {
        let state: DocumentVisibilityState = 'visible';
        Object.defineProperty(document, 'visibilityState', {
            configurable: true,
            get: () => state,
        });
        const { container } = render(<SetupVideo files={files} label="demo" />);
        playing(container);

        fireEvent(document, new Event('visibilitychange'));
        expect(pause).not.toHaveBeenCalled();

        state = 'hidden';
        fireEvent(document, new Event('visibilitychange'));
        expect(pause).toHaveBeenCalledTimes(1);
    });

    it('when the window loses focus', () => {
        const { container } = render(<SetupVideo files={files} label="demo" />);
        playing(container);

        fireEvent.blur(window);

        expect(pause).toHaveBeenCalledTimes(1);
    });

    it('when it scrolls out of view, and not while in view', () => {
        const { container } = render(<SetupVideo files={files} label="demo" />);
        playing(container);
        const [view] = observed;
        expect(view.target).toBe(container.querySelector('video')!.parentElement);
        const report = (isIntersecting: boolean) =>
            view.callback(
                [{ isIntersecting } as IntersectionObserverEntry],
                {} as IntersectionObserver,
            );

        report(true);
        expect(pause).not.toHaveBeenCalled();

        report(false);
        expect(pause).toHaveBeenCalledTimes(1);
    });

    it('leaves a paused video alone and never starts it again', () => {
        const { container } = render(<SetupVideo files={files} label="demo" />);

        fireEvent.blur(window);
        expect(pause).not.toHaveBeenCalled();

        playing(container);
        fireEvent.blur(window);
        fireEvent.focus(window);
        fireEvent(document, new Event('visibilitychange'));
        expect(pause).toHaveBeenCalledTimes(1);
        expect(play).not.toHaveBeenCalled();
    });

    it('stops listening once it is gone', async () => {
        const { container, unmount } = render(
            <div data-testid="holder">
                <SetupVideo files={files} label="demo" />
            </div>,
        );
        const video = playing(container);
        const holder = container.firstElementChild as HTMLElement;
        unmount();
        pause.mockClear();
        Object.defineProperty(video, 'paused', { configurable: true, get: () => false });

        fireEvent.blur(window);
        holder.setAttribute('hidden', '');
        await act(async () => {});

        expect(pause).not.toHaveBeenCalled();
        expect(observed[0].disconnected).toBe(true);
    });
});

import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import Plyr from 'plyr';
import 'plyr/dist/plyr.css';

import { cn } from '@/shared/lib/utils';

/**
 * Plyr's icon sprite, served from here. Its default is cdn.plyr.io, which the
 * content security policy (default-src 'none', everything else 'self') blocks,
 * leaving every control without its icon. The package does not export the
 * file, so a copy of the pinned version's sprite sits in public/vendor; a test
 * holds it to the installed one byte for byte.
 */
export const PLYR_SPRITE = '/vendor/plyr-3.8.4.svg';

/**
 * The player's settings. blankVideo is what Plyr loads into the element on
 * destroy to stop its download; its default is on the CDN too. An empty source
 * starts no request at all.
 */
export const PLYR_OPTIONS: Plyr.Options = {
    iconUrl: PLYR_SPRITE,
    loadSprite: true,
    blankVideo: '',
    controls: ['play-large', 'play', 'progress', 'current-time', 'mute', 'volume', 'fullscreen'],
    // The videos have a quiet music bed; they play only after the reader presses play.
    muted: false,
    fullscreen: { enabled: true, iosNative: true },
};

/**
 * The player's own words — its buttons' names and the progress bar's — that
 * the controls in PLYR_OPTIONS use. Plyr ships them in English only; each is
 * looked up as plyr.<key> in the interface's own translations. Plyr's
 * placeholders are single-braced, {currentTime}, so i18next leaves them be.
 */
export const PLYR_LABEL_KEYS = [
    'play',
    'pause',
    'restart',
    'seek',
    'seekLabel',
    'played',
    'buffered',
    'currentTime',
    'duration',
    'volume',
    'mute',
    'unmute',
    'enterFullscreen',
    'exitFullscreen',
    'frameTitle',
] as const;

/** The language a how-to video is recorded in, for an interface language. */
export type VideoLanguage = 'ru' | 'en';

export const videoLanguage = (language: string): VideoLanguage =>
    language.startsWith('ru') ? 'ru' : 'en';

/** A how-to video and its poster. */
export type VideoFiles = { src: string; poster: string };

/**
 * useFileExists asks for a file the way an <img> would and answers whether it
 * came: null while asking. A <video> says nothing when its poster fails to
 * load, so the poster is asked for separately; the browser answers the
 * player's own request for it from cache.
 */
export const useFileExists = (url: string): boolean | null => {
    const [answer, setAnswer] = useState<{ url: string; exists: boolean } | null>(null);
    useEffect(() => {
        const probe = new Image();
        probe.onload = () => setAnswer({ url, exists: true });
        probe.onerror = () => setAnswer({ url, exists: false });
        probe.src = url;
        return () => {
            probe.onload = null;
            probe.onerror = null;
        };
    }, [url]);
    return answer?.url === url ? answer.exists : null;
};

type SetupVideoProps = {
    /** The video and its poster in the given language. */
    files: (language: VideoLanguage) => VideoFiles;
    /** The player's accessible name. */
    label: string;
    /**
     * The illustration that stands in for a missing video or poster. Without
     * one the video is optional: nothing shows until it has loaded, and a
     * missing video leaves nothing behind.
     */
    illustration?: string;
    /** Told once the video turns out to be missing. */
    onMissing?: () => void;
    'data-testid'?: string;
    className?: string;
};

/**
 * SetupVideo is a silent how-to: a short screen recording beside the steps it
 * shows.
 *
 * The videos are made separately from the interface and may not be there
 * yet — or not in this language. A missing video gives way to the
 * illustration rather than an empty player (or to nothing, when there is no
 * illustration), and a missing poster to the same illustration rather than a
 * black frame.
 *
 * Played through Plyr. The <video> is made here rather than by React: Plyr
 * wraps the element in its own markup and, on destroy, puts a copy of the
 * original back in place of that wrapper — so a React-owned element would be
 * gone from under React the first time the player went away. React owns only
 * the empty box the player is built in.
 */
const SetupVideo: React.FC<SetupVideoProps> = ({
    files,
    label,
    illustration,
    onMissing,
    'data-testid': testId,
    className,
}) => {
    const { t, i18n } = useTranslation();
    const { src, poster } = files(videoLanguage(i18n.language ?? ''));
    const host = useRef<HTMLDivElement>(null);
    const optional = illustration === undefined;
    // A change of language builds the player again with the new words, as it
    // already does for the other language's video.
    const labels = useMemo(
        () => Object.fromEntries(PLYR_LABEL_KEYS.map((key) => [key, t(`plyr.${key}`)])),
        [t],
    );

    // Keyed by the source so a change of language starts over: the other
    // language's video may well exist where this one did not.
    const [failedSrc, setFailedSrc] = useState<string | null>(null);
    const [loadedSrc, setLoadedSrc] = useState<string | null>(null);
    const posterFound = useFileExists(poster);
    const posterSrc = posterFound === false ? (illustration ?? '') : poster;
    const failed = failedSrc === src;
    useEffect(() => {
        if (failed) onMissing?.();
    }, [failed, onMissing]);

    useEffect(() => {
        const box = host.current;
        if (!box || failed) return;

        const video = document.createElement('video');
        // Before the source, which starts the request.
        video.preload = 'metadata';
        if (posterSrc) video.poster = posterSrc;
        video.controls = true;
        // Reflected as the playsinline attribute: iOS plays it in the page
        // rather than taking it to full screen.
        video.playsInline = true;
        video.setAttribute('aria-label', label);
        video.className = 'aspect-video w-full';
        video.src = src;
        const onError = () => setFailedSrc(src);
        const onLoaded = () => setLoadedSrc(src);
        video.addEventListener('error', onError);
        video.addEventListener('loadedmetadata', onLoaded);
        box.appendChild(video);

        let player: Plyr | null = null;
        try {
            player = new Plyr(video, { ...PLYR_OPTIONS, i18n: labels });
        } catch {
            // A browser Plyr does not support keeps the native player.
        }

        return () => {
            video.removeEventListener('error', onError);
            video.removeEventListener('loadedmetadata', onLoaded);
            player?.destroy();
            box.replaceChildren();
        };
    }, [src, posterSrc, label, labels, failed]);

    const frame = cn('aspect-video w-full overflow-hidden rounded-md bg-muted', className);

    if (failed) {
        if (optional) return null;
        return <img src={illustration} alt="" className={cn(frame, 'object-cover')} />;
    }
    return (
        <div
            ref={host}
            data-testid={testId}
            // An optional video stays out of sight until it is known to play.
            hidden={optional && loadedSrc !== src}
            // Plyr's accent, in the site's own palette.
            style={{ '--plyr-color-main': 'var(--primary)' } as React.CSSProperties}
            className={frame}
        />
    );
};

export default SetupVideo;

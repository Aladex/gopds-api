import React, { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import Plyr from 'plyr';
import 'plyr/dist/plyr.css';

import { cn } from '@/shared/lib/utils';

/** The illustration that stands in wherever the video cannot. */
export const KINDLE_ILLUSTRATION = '/images/kindle-setup.webp';

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
    controls: ['play-large', 'play', 'progress', 'current-time', 'fullscreen'],
    muted: true,
    fullscreen: { enabled: true, iosNative: true },
};

/** The how-to video and its poster, one per interface language. */
const videoFor = (language: string) => {
    const lang = language.startsWith('ru') ? 'ru' : 'en';
    return { src: `/videos/kindle-setup-${lang}.mp4`, poster: `/videos/kindle-setup-${lang}.jpg` };
};

/**
 * KindleVideo is the silent how-to: setting up Amazon's approved list and the
 * profile, then sending a book.
 *
 * The video is made separately from the interface and may not be there yet —
 * or not in this language. A missing video gives way to the illustration
 * rather than an empty player, and a missing poster to the same illustration
 * rather than a black frame.
 *
 * Played through Plyr. The <video> is made here rather than by React: Plyr
 * wraps the element in its own markup and, on destroy, puts a copy of the
 * original back in place of that wrapper — so a React-owned element would be
 * gone from under React the first time the player went away. React owns only
 * the empty box the player is built in.
 */
const KindleVideo: React.FC<{ className?: string }> = ({ className }) => {
    const { t, i18n } = useTranslation();
    const { src, poster } = videoFor(i18n.language ?? '');
    const label = t('kindle.videoLabel');
    const host = useRef<HTMLDivElement>(null);

    // Keyed by the source so a change of language starts over: the other
    // language's video may well exist where this one did not.
    const [failedSrc, setFailedSrc] = useState<string | null>(null);
    const [missingPoster, setMissingPoster] = useState<string | null>(null);
    const posterSrc = missingPoster === poster ? KINDLE_ILLUSTRATION : poster;
    const failed = failedSrc === src;

    // A <video> says nothing when its poster fails to load, so the poster is
    // asked for separately; the browser answers the second request from cache.
    useEffect(() => {
        const probe = new Image();
        probe.onerror = () => setMissingPoster(poster);
        probe.src = poster;
        return () => {
            probe.onerror = null;
        };
    }, [poster]);

    useEffect(() => {
        const box = host.current;
        if (!box || failed) return;

        const video = document.createElement('video');
        // Before the source, which starts the request.
        video.preload = 'metadata';
        video.poster = posterSrc;
        video.controls = true;
        video.muted = true;
        // Reflected as the playsinline attribute: iOS plays it in the page
        // rather than taking it to full screen.
        video.playsInline = true;
        video.setAttribute('aria-label', label);
        video.className = 'aspect-video w-full';
        video.src = src;
        const onError = () => setFailedSrc(src);
        video.addEventListener('error', onError);
        box.appendChild(video);

        let player: Plyr | null = null;
        try {
            player = new Plyr(video, PLYR_OPTIONS);
        } catch {
            // A browser Plyr does not support keeps the native player.
        }

        return () => {
            video.removeEventListener('error', onError);
            player?.destroy();
            box.replaceChildren();
        };
    }, [src, posterSrc, label, failed]);

    const frame = cn('aspect-video w-full overflow-hidden rounded-md bg-muted', className);

    if (failed) {
        return <img src={KINDLE_ILLUSTRATION} alt="" className={cn(frame, 'object-cover')} />;
    }
    return (
        <div
            ref={host}
            data-testid="kindle-video"
            // Plyr's accent, in the site's own palette.
            style={{ '--plyr-color-main': 'var(--primary)' } as React.CSSProperties}
            className={frame}
        />
    );
};

export default KindleVideo;

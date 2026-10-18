import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { cn } from '@/shared/lib/utils';

/** The illustration that stands in wherever the video cannot. */
export const KINDLE_ILLUSTRATION = '/images/kindle-setup.webp';

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
 */
const KindleVideo: React.FC<{ className?: string }> = ({ className }) => {
    const { t, i18n } = useTranslation();
    const { src, poster } = videoFor(i18n.language ?? '');

    // Keyed by the source so a change of language starts over: the other
    // language's video may well exist where this one did not.
    const [failedSrc, setFailedSrc] = useState<string | null>(null);
    const [missingPoster, setMissingPoster] = useState<string | null>(null);
    const posterSrc = missingPoster === poster ? KINDLE_ILLUSTRATION : poster;

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

    const frame = cn('aspect-video w-full rounded-md bg-muted object-cover', className);

    if (failedSrc === src) {
        return <img src={KINDLE_ILLUSTRATION} alt="" className={frame} />;
    }
    return (
        <video
            key={src}
            src={src}
            poster={posterSrc}
            controls
            playsInline
            muted
            preload="metadata"
            aria-label={t('kindle.videoLabel')}
            onError={() => setFailedSrc(src)}
            className={frame}
        />
    );
};

export default KindleVideo;

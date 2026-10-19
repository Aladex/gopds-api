import React from 'react';
import { useTranslation } from 'react-i18next';

import SetupVideo, { type VideoLanguage } from '@/shared/components/SetupVideo';

export { PLYR_LABEL_KEYS, PLYR_OPTIONS, PLYR_SPRITE } from '@/shared/components/SetupVideo';

/** The illustration that stands in wherever the video cannot. */
export const KINDLE_ILLUSTRATION = '/images/kindle-setup.webp';

/** The how-to video and its poster, one per interface language. */
const kindleVideo = (lang: VideoLanguage) => ({
    src: `/videos/kindle-setup-${lang}.mp4`,
    poster: `/videos/kindle-setup-${lang}.jpg`,
});

/**
 * KindleVideo is the silent how-to: setting up Amazon's approved list and the
 * profile, then sending a book. Where the video or its poster is missing, the
 * illustration stands in.
 */
const KindleVideo: React.FC<{ className?: string }> = ({ className }) => {
    const { t } = useTranslation();
    return (
        <SetupVideo
            files={kindleVideo}
            label={t('kindle.videoLabel')}
            illustration={KINDLE_ILLUSTRATION}
            data-testid="kindle-video"
            className={className}
        />
    );
};

export default KindleVideo;

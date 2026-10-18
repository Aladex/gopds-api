import React from 'react';
import { fireEvent, render } from '@testing-library/react';

import KindleVideo, { KINDLE_ILLUSTRATION } from '@/features/kindle/KindleVideo';

// The video is made apart from the interface and may be missing, or missing in
// one language. Neither may leave an empty player behind.

const translation = { t: (key: string) => key, i18n: { language: 'ru' } };
vi.mock('react-i18next', () => ({ useTranslation: () => translation }));

beforeEach(() => {
    translation.i18n.language = 'ru';
});

it('plays the video in the interface language, silent and inline', () => {
    const { container } = render(<KindleVideo />);
    const video = container.querySelector('video')!;

    expect(video.getAttribute('src')).toBe('/videos/kindle-setup-ru.mp4');
    expect(video.getAttribute('poster')).toBe('/videos/kindle-setup-ru.jpg');
    expect(video.muted).toBe(true);
    expect(video).toHaveAttribute('playsinline');
    expect(video).toHaveAttribute('controls');
    expect(video).toHaveAttribute('preload', 'metadata');
});

it('takes the English video for an English interface', () => {
    translation.i18n.language = 'en';
    const { container } = render(<KindleVideo />);

    expect(container.querySelector('video')!.getAttribute('src')).toBe(
        '/videos/kindle-setup-en.mp4',
    );
});

it('gives way to the illustration when the video is missing', () => {
    const { container } = render(<KindleVideo />);

    fireEvent.error(container.querySelector('video')!);

    expect(container.querySelector('video')).toBeNull();
    expect(container.querySelector('img')!.getAttribute('src')).toBe(KINDLE_ILLUSTRATION);
});

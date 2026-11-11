import { act, fireEvent, render, screen, within } from '@testing-library/react';
import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';

import PipelineCarousel from '@/features/admin/scan/PipelineCarousel';
import { derivePipeline, type StageKey } from '@/features/admin/scan/pipeline';
import en from '@/locales/en/translation.json';
import ru from '@/locales/ru/translation.json';
import { healthy, twoProblems } from './pipelineFixtures';

/*
 * The phone's chain: a row of dots that is the carousel's indicator, one line
 * per problem under it, and a snapping track of stage cards. The pipeline
 * comes from the same fixtures the derivation is tested with, and the real
 * Russian catalogue renders the text.
 *
 * jsdom lays nothing out, so every slide is given a place before the
 * carousel mounts: 16 px inset, 110 px apart, and scrollTo is a spy.
 */

const INSET = 16;
const PITCH = 110;

beforeAll(async () => {
    await i18n.use(initReactI18next).init({
        resources: { en: { translation: en }, ru: { translation: ru } },
        lng: 'ru',
        keySeparator: false,
        interpolation: { escapeValue: false },
    });
});

let scrollTo: ReturnType<typeof vi.fn>;
let restoreGeometry: () => void;

beforeEach(() => {
    const offsetLeft = Object.getOwnPropertyDescriptor(HTMLElement.prototype, 'offsetLeft');
    Object.defineProperty(HTMLElement.prototype, 'offsetLeft', {
        configurable: true,
        get(this: HTMLElement) {
            const slide = this.dataset.slide;
            return slide === undefined ? 0 : INSET + Number(slide) * PITCH;
        },
    });
    const original = Element.prototype.scrollTo;
    scrollTo = vi.fn();
    Element.prototype.scrollTo = scrollTo as never;
    restoreGeometry = () => {
        if (offsetLeft) {
            Object.defineProperty(HTMLElement.prototype, 'offsetLeft', offsetLeft);
        }
        Element.prototype.scrollTo = original;
    };
});

afterEach(() => {
    restoreGeometry();
});

const draw = (input: 'healthy' | 'twoProblems', selected?: StageKey | null) => {
    const now = new Date();
    const pipeline = derivePipeline(input === 'healthy' ? healthy(now) : twoProblems(now), now);
    const onSelect = vi.fn();
    const view = render(
        <PipelineCarousel
            pipeline={pipeline}
            selected={selected === undefined ? pipeline.defaultStage : selected}
            onSelect={onSelect}
        />,
    );
    return { pipeline, onSelect, view };
};

const dotsRow = () => screen.getByRole('group', { name: ru['scanPanel.carousel.dots'] });
const problemLines = () =>
    screen.queryAllByRole('button').filter((button) => button.hasAttribute('data-problem'));

describe('PipelineCarousel', () => {
    test('two problems show two lines under the dots', () => {
        draw('twoProblems');

        const lines = problemLines();
        expect(lines.map((line) => line.getAttribute('data-problem'))).toEqual(['model', 'arch']);
        expect(lines[0]).toHaveTextContent('Модель');
        expect(lines[1]).toHaveTextContent('Архивы');
        // Under the dots, in the document's order.
        const row = dotsRow();
        for (const line of lines) {
            expect(
                row.compareDocumentPosition(line) & Node.DOCUMENT_POSITION_FOLLOWING,
            ).toBeTruthy();
        }
    });

    test('a problem line selects its stage', () => {
        const { onSelect } = draw('twoProblems');

        fireEvent.click(problemLines()[1]);

        expect(onSelect).toHaveBeenCalledWith('arch');
    });

    test('the selected dot shows its name', () => {
        draw('twoProblems', 'model');

        const row = dotsRow();
        expect(within(row).getByText('Модель')).toBeVisible();
        // Only the selected one is named: the others are dots.
        expect(within(row).queryByText('Архивы')).toBeNull();
        const dot = within(row)
            .getAllByRole('button')
            .find((button) => button.getAttribute('data-dot') === 'model');
        expect(dot).toHaveAttribute('aria-current', 'true');
    });

    test('tapping a dot selects its stage', () => {
        const { onSelect } = draw('healthy');

        const names = within(dotsRow())
            .getAllByRole('button')
            .find((button) => button.getAttribute('data-dot') === 'names');
        fireEvent.click(names!);

        expect(onSelect).toHaveBeenCalledWith('names');
    });

    test('a chosen stage moves the track to its card', () => {
        const { view, pipeline, onSelect } = draw('healthy');
        scrollTo.mockClear();

        view.rerender(
            <PipelineCarousel pipeline={pipeline} selected="names" onSelect={onSelect} />,
        );

        expect(scrollTo).toHaveBeenCalledWith(
            expect.objectContaining({ left: 2 * PITCH, behavior: 'smooth' }),
        );
    });

    test('opens on the stuck stage', () => {
        draw('twoProblems');

        // «Модель» is the fourth card; the page opens on it without a glide.
        expect(scrollTo).toHaveBeenCalledWith({ left: 3 * PITCH, behavior: 'auto' });
        const card = document.querySelector('[data-slide="3"] [data-stage-node]');
        expect(card).toHaveAttribute('data-stage-node', 'model');
        expect(card).toHaveAttribute('aria-current', 'true');
        expect(card).toHaveTextContent('4 / 6');
    });

    test('healthy opens on the human card and shows no problem lines', () => {
        draw('healthy');

        expect(scrollTo).toHaveBeenCalledWith({ left: 5 * PITCH, behavior: 'auto' });
        expect(problemLines()).toHaveLength(0);
        const human = within(dotsRow())
            .getAllByRole('button')
            .find((button) => button.getAttribute('data-dot') === 'human');
        expect(human).toHaveAttribute('aria-current', 'true');
    });

    test('waits for a stage before it moves the track', () => {
        const { view, pipeline, onSelect } = draw('twoProblems', null);
        expect(scrollTo).not.toHaveBeenCalled();

        view.rerender(
            <PipelineCarousel pipeline={pipeline} selected="model" onSelect={onSelect} />,
        );

        // The first stage known is where the page opens, still without a glide.
        expect(scrollTo).toHaveBeenCalledWith({ left: 3 * PITCH, behavior: 'auto' });
    });

    test('a swipe that comes to rest selects its stage without a history entry', () => {
        vi.useFakeTimers();
        try {
            const { onSelect } = draw('healthy');
            const track = screen.getByRole('group', { name: ru['scanPanel.carousel.track'] });
            track.getBoundingClientRect = () => ({ left: 0, width: 300 }) as DOMRect;
            track.style.paddingLeft = `${INSET}px`;
            track.querySelectorAll<HTMLElement>('[data-slide]').forEach((slide) => {
                slide.getBoundingClientRect = () =>
                    ({
                        left: INSET + Number(slide.dataset.slide) * PITCH - track.scrollLeft,
                        width: 100,
                    }) as DOMRect;
            });

            track.scrollLeft = 2 * PITCH;
            fireEvent.scroll(track);
            act(() => vi.advanceTimersByTime(500));

            expect(onSelect).toHaveBeenCalledWith('names', { replace: true });
        } finally {
            vi.useRealTimers();
        }
    });
});

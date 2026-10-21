import { createLoop, createOwner, listenerBag } from '../lifecycle';

/** A requestAnimationFrame that only runs when the test says so. */
function fakeFrames() {
    let id = 0;
    const pending = new Map<number, FrameRequestCallback>();
    return {
        raf: (cb: FrameRequestCallback) => {
            pending.set(++id, cb);
            return id;
        },
        caf: (n: number) => void pending.delete(n),
        /** Run every frame currently scheduled. */
        tick(now: number) {
            const due = [...pending.values()];
            pending.clear();
            due.forEach((cb) => cb(now));
        },
        get pending() {
            return pending.size;
        },
    };
}

function setHidden(hidden: boolean) {
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => hidden });
    document.dispatchEvent(new Event('visibilitychange'));
}

afterEach(() => setHidden(false));

describe('createLoop', () => {
    it('renders a frame per animation frame once started', () => {
        const frames = fakeFrames();
        const frame = vi.fn();
        const loop = createLoop(frame, frames);

        loop.start();
        frames.tick(16);
        frames.tick(32);

        expect(frame).toHaveBeenCalledTimes(2);
        loop.dispose();
    });

    it('stops asking for frames while the tab is hidden, and picks up when it is back', () => {
        const frames = fakeFrames();
        const frame = vi.fn();
        const loop = createLoop(frame, frames);
        loop.start();

        setHidden(true);
        expect(frames.pending).toBe(0);
        frames.tick(16);
        expect(frame).not.toHaveBeenCalled();

        setHidden(false);
        expect(frames.pending).toBe(1);
        frames.tick(32);
        expect(frame).toHaveBeenCalledTimes(1);
        loop.dispose();
    });

    it('leaves nothing scheduled and nothing listening after dispose', () => {
        const frames = fakeFrames();
        const frame = vi.fn();
        const loop = createLoop(frame, frames);
        loop.start();

        loop.dispose();
        expect(frames.pending).toBe(0);

        // Coming back to the tab must not resurrect a disposed loop.
        setHidden(true);
        setHidden(false);
        expect(frames.pending).toBe(0);
        frames.tick(16);
        expect(frame).not.toHaveBeenCalled();
    });

    it('draws one frame and never asks for another when motion is reduced', () => {
        const frames = fakeFrames();
        const raf = vi.spyOn(frames, 'raf');
        const frame = vi.fn();
        const loop = createLoop(frame, frames, { still: true });

        loop.start();
        expect(frame).toHaveBeenCalledTimes(1);
        // Not even after the tab has been away and come back.
        setHidden(true);
        setHidden(false);
        frames.tick(16);
        expect(raf).not.toHaveBeenCalled();
        expect(frame).toHaveBeenCalledTimes(1);
        loop.dispose();
    });

    it('takes its visibility listener down with it', () => {
        const add = vi.spyOn(document, 'addEventListener');
        const remove = vi.spyOn(document, 'removeEventListener');
        const loop = createLoop(vi.fn(), fakeFrames());
        const listener = add.mock.calls.find(([type]) => type === 'visibilitychange')?.[1];
        expect(listener).toBeDefined();

        loop.dispose();

        expect(remove).toHaveBeenCalledWith('visibilitychange', listener);
        add.mockRestore();
        remove.mockRestore();
    });

    it('never starts a second chain when started twice', () => {
        const frames = fakeFrames();
        const loop = createLoop(vi.fn(), frames);
        loop.start();
        loop.start();
        expect(frames.pending).toBe(1);
        loop.dispose();
    });
});

describe('listenerBag', () => {
    it('removes every listener it added', () => {
        const target = new EventTarget();
        const add = vi.spyOn(target, 'addEventListener');
        const remove = vi.spyOn(target, 'removeEventListener');
        const handler = vi.fn();
        const bag = listenerBag();

        bag.on(target, 'a', handler);
        bag.on(target, 'b', handler, { passive: true });
        target.dispatchEvent(new Event('a'));
        expect(handler).toHaveBeenCalledTimes(1);

        bag.clear();
        target.dispatchEvent(new Event('a'));
        target.dispatchEvent(new Event('b'));
        expect(handler).toHaveBeenCalledTimes(1);
        expect(remove).toHaveBeenCalledTimes(add.mock.calls.length);
    });
});

describe('createOwner', () => {
    it('runs clean-up in reverse order of registration', () => {
        const order: string[] = [];
        const owner = createOwner();
        owner.add(() => order.push('context'));
        owner.add(() => order.push('renderer'));
        owner.add(() => order.push('loop'));

        owner.dispose();

        // What was made last depends on what was made first, so it goes first.
        expect(order).toEqual(['loop', 'renderer', 'context']);
    });

    it('runs each step once, however many times it is disposed', () => {
        const step = vi.fn();
        const owner = createOwner();
        owner.add(step);

        owner.dispose();
        owner.dispose();

        expect(step).toHaveBeenCalledTimes(1);
        expect(owner.disposed).toBe(true);
    });

    it('keeps going when one step throws, so the rest is still released', () => {
        const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
        const later = vi.fn();
        const owner = createOwner();
        owner.add(later);
        owner.add(() => {
            throw new Error('broken');
        });

        expect(() => owner.dispose()).not.toThrow();
        expect(later).toHaveBeenCalledTimes(1);
        warn.mockRestore();
    });

    it('disposes a tracked resource once, even when tracked twice', () => {
        const resource = { dispose: vi.fn() };
        const owner = createOwner();

        expect(owner.track(resource)).toBe(resource);
        owner.track(resource);
        owner.dispose();

        expect(resource.dispose).toHaveBeenCalledTimes(1);
    });

    it('releases at once anything handed to it after it was disposed', () => {
        const owner = createOwner();
        owner.dispose();
        const resource = { dispose: vi.fn() };

        owner.track(resource);

        expect(resource.dispose).toHaveBeenCalledTimes(1);
    });
});

describe('createLoop: pacing', () => {
    it('draws every refresh of a 60 Hz display', () => {
        const frames = fakeFrames();
        const drawn: number[] = [];
        const loop = createLoop((now) => drawn.push(now), frames);
        loop.start();
        for (let i = 1; i <= 60; i++) frames.tick(i * (1000 / 60));

        expect(drawn).toHaveLength(60);
        loop.dispose();
    });

    it('caps a 144 Hz display at 60 frames a second', () => {
        const frames = fakeFrames();
        const drawn: number[] = [];
        const loop = createLoop((now) => drawn.push(now), frames);
        loop.start();
        for (let i = 1; i <= 144; i++) frames.tick(i * (1000 / 144));

        // One second of refreshes: about sixty drawn, never two closer than a 60 Hz frame.
        expect(drawn.length).toBeGreaterThanOrEqual(45);
        expect(drawn.length).toBeLessThanOrEqual(61);
        for (let i = 1; i < drawn.length; i++)
            expect(drawn[i] - drawn[i - 1]).toBeGreaterThanOrEqual(1000 / 60 - 2);
        loop.dispose();
    });

    it('reports the interval since the last drawn frame, and none after a gap it caused', () => {
        const frames = fakeFrames();
        const intervals: (number | null)[] = [];
        const loop = createLoop((_, interval) => intervals.push(interval), frames);
        loop.start();
        frames.tick(100);
        frames.tick(120);
        frames.tick(140);
        loop.hold(1);
        frames.tick(160);
        frames.tick(180);
        frames.tick(200);

        // The first frame has nothing before it; the one after a hold has a
        // gap that says nothing about how long a frame takes.
        expect(intervals).toEqual([null, 20, 20, null, 20]);
        loop.dispose();
    });

    it('holds off drawing for the frames it is asked to, then carries on', () => {
        const frames = fakeFrames();
        const drawn: number[] = [];
        const loop = createLoop((now) => drawn.push(now), frames);
        loop.start();
        frames.tick(20);

        loop.hold(2);
        frames.tick(40);
        frames.tick(60);
        frames.tick(80);

        expect(drawn).toEqual([20, 80]);
        loop.dispose();
    });

    it('stops for good when told to, keeping whatever was drawn last', () => {
        const frames = fakeFrames();
        const frame = vi.fn();
        const loop = createLoop(frame, frames);
        loop.start();
        frames.tick(20);

        loop.stop();
        frames.tick(40);
        setHidden(true);
        setHidden(false);
        frames.tick(60);

        expect(frame).toHaveBeenCalledTimes(1);
        expect(frames.pending).toBe(0);
        loop.dispose();
    });
});

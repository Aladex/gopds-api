import type { SceneHandle, SceneStats } from '@/features/auth/scene/createScene';

/**
 * The room host's line to whatever scene is live: one stable object, made
 * once per host, so passing it down never changes the scene's props.
 *
 * It outlives any one scene. The host keeps the room for a page's lifetime,
 * but a scene is rebuilt when the phone turns, and disposed when the reader
 * goes into the application and comes back later; the quality level the last
 * one settled on is kept here, so the next one starts there instead of
 * finding it out again the slow way.
 */
export interface RoomControl {
    /** The live scene, or null while there is none. */
    attach(handle: SceneHandle | null): void;
    /** Let the screen that is about to paint go first. */
    hold(): void;
    stats(): SceneStats | null;
    /** Handed to every new scene as its quality option. */
    quality: { start?: number; onChange: (level: number) => void };
}

export function createRoomControl(): RoomControl {
    let live: SceneHandle | null = null;
    const quality: RoomControl['quality'] = {
        start: undefined,
        onChange: (level: number) => {
            quality.start = level;
        },
    };
    return {
        attach(handle) {
            live = handle;
        },
        hold() {
            live?.hold();
        },
        stats() {
            return live?.stats() ?? null;
        },
        quality,
    };
}

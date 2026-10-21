import React, { useEffect, useState } from 'react';

import type { SceneStats } from '@/features/auth/scene/createScene';
import type { RoomControl } from '@/features/auth/scene/roomControl';
import { touchFirst } from '@/features/auth/scene/viewport';

/**
 * An opt-in readout of how the room is doing on this device, for a reader who
 * says the sign-in screens are slow and can send a screenshot: open any of
 * them with ?roomdebug=1. Without the flag nothing of this is rendered.
 *
 * Plain numbers in a corner, out of the way of the form and of the pointer.
 */

export const wantsRoomDebug = (search: string) =>
    new URLSearchParams(search).get('roomdebug') === '1';

type RoomDebugProps = {
    control: RoomControl;
    /** Milliseconds from the last screen change to its second painted frame. */
    lastSwitch: React.RefObject<number | null>;
};

const RoomDebug: React.FC<RoomDebugProps> = ({ control, lastSwitch }) => {
    const [stats, setStats] = useState<SceneStats | null>(null);
    const [switchMs, setSwitchMs] = useState<number | null>(null);

    useEffect(() => {
        const read = () => {
            setStats(control.stats());
            setSwitchMs(lastSwitch.current);
        };
        const id = setInterval(read, 500);
        return () => clearInterval(id);
    }, [control, lastSwitch]);

    const rows: [string, string][] = [
        ['fps', stats ? String(stats.fps) : '—'],
        ['level', stats?.level ?? 'no scene'],
        [
            'build',
            stats?.buildMs != null
                ? `${stats.buildMs} ms (assets ${stats.phases.assets} · pages ${stats.phases.pages} · gpu ${stats.phases.gpu})`
                : '—',
        ],
        ['switch', switchMs != null ? `${Math.round(switchMs)} ms` : '—'],
        ['gpu', stats?.renderer ?? '—'],
        ['ratio', stats ? String(stats.pixelRatio) : '—'],
        ['canvas', stats?.canvas ?? '—'],
        // Which composition, and whether the keyboard rule applies here: on a
        // phone that answer should be touch, or the keyboard rebuilds the room.
        ['layout', stats ? `${stats.layout} · ${touchFirst() ? 'touch' : 'pointer'}` : '—'],
    ];

    return (
        <div
            data-room-debug
            aria-hidden
            className="pointer-events-none fixed bottom-2 left-2 z-[60] max-w-[calc(100vw-1rem)] rounded-md bg-black/70 px-2 py-1 text-[11px] leading-4 text-white tabular-nums"
        >
            {rows.map(([name, value]) => (
                <div key={name} className="flex gap-2">
                    <span className="w-12 shrink-0 text-white/60">{name}</span>
                    <span className="break-all">{value}</span>
                </div>
            ))}
        </div>
    );
};

export default RoomDebug;

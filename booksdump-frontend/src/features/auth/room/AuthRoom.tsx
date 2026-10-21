import React, {
    lazy,
    Suspense,
    createContext,
    useCallback,
    useContext,
    useEffect,
    useMemo,
    useRef,
    useState,
} from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

// The recording's player, its Easter eggs and their positions load on their
// own, a moment after the screen: the room is dark until then, which is what
// it looks like anyway, and the main bundle does not carry any of it.
const RoomVideo = lazy(() => import('@/features/auth/room/RoomVideo'));

/**
 * One reading room for every public screen.
 *
 * Sign-in, the forgotten password, registration, the two token links and the
 * error page all stand in the same room — one recording of it, playing on a
 * loop (RoomVideo). It lives here, above the routes, so moving from one screen
 * to the next keeps the same video playing instead of starting it over; the
 * screens only say that they want it.
 *
 * A screen wants the room while it is mounted (useAuthRoom). The room exists
 * while at least one screen wants it, and for a short grace after the last one
 * goes. A route switch unmounts the old screen and mounts the new one in the
 * same commit, so the count never visibly reaches zero; the grace is for
 * anything slower — a screen that renders nothing for a moment while it
 * decides, a redirect on its way — and for a reader who steps out and straight
 * back. Once nobody wants the room it is hidden at once, so the application
 * never shows through to it, and disposed when the grace runs out.
 *
 * The book on the error page has a page torn out; any screen that asks for the
 * torn book gets that recording, on the same video element.
 */

/**
 * How long the room outlives the last screen that wanted it. Long enough to
 * cover a redirect or a screen that renders nothing for a frame or two, and a
 * quick step out and back; short enough that the video is gone soon after the
 * reader has gone into the application, which is busy loading its own first
 * page at that moment anyway.
 */
export const ROOM_GRACE_MS = 1000;

interface RoomRegistry {
    /** Say a screen wants the room; returns what to call when it no longer does. */
    enter(torn: boolean): () => void;
}

const AuthRoomContext = createContext<RoomRegistry | null>(null);

export const AuthRoomProvider: React.FC<React.PropsWithChildren> = ({ children }) => {
    const { t } = useTranslation();
    const navigate = useNavigate();
    // Somebody wants the room: it is on show.
    const [wanted, setWanted] = useState(false);
    // The room exists: wanted, or still inside the grace.
    const [alive, setAlive] = useState(false);
    const [torn, setTorn] = useState(false);
    const screens = useRef(new Map<number, boolean>());
    const nextId = useRef(0);
    const grace = useRef<ReturnType<typeof setTimeout> | null>(null);
    // Set when the host itself goes. Its screens leave after it, and each
    // leaving would otherwise start a grace that nobody is left to need.
    const closed = useRef(false);

    const registry = useMemo<RoomRegistry>(() => {
        const cancelGrace = () => {
            if (grace.current !== null) clearTimeout(grace.current);
            grace.current = null;
        };
        const sync = () => {
            if (closed.current) return;
            const count = screens.current.size;
            setWanted(count > 0);
            setTorn([...screens.current.values()].some(Boolean));
            if (count > 0) {
                cancelGrace();
                setAlive(true);
                return;
            }
            cancelGrace();
            grace.current = setTimeout(() => {
                grace.current = null;
                setAlive(false);
            }, ROOM_GRACE_MS);
        };
        return {
            enter(wantsTorn) {
                const id = nextId.current++;
                screens.current.set(id, wantsTorn);
                sync();
                return () => {
                    screens.current.delete(id);
                    sync();
                };
            },
        };
    }, []);

    // Nothing to wait for once the host itself is gone.
    useEffect(() => {
        // Under React's development double-mount the host is taken down and
        // put back before its screens are, so their leaving and re-entering in
        // between is ignored and the room's state is already right.
        closed.current = false;
        return () => {
            closed.current = true;
            if (grace.current !== null) clearTimeout(grace.current);
            grace.current = null;
        };
    }, []);

    // Stable, like everything else handed to the room: the host re-renders
    // on every navigation, and the room is memoised so it does not. The
    // router hands out a new navigate function per location, so the handler
    // reads the current one through a ref instead of depending on it.
    const latest = useRef({ navigate, t });
    useEffect(() => {
        latest.current = { navigate, t };
    }, [navigate, t]);
    const onGolden = useCallback(() => {
        // The golden post is worth an invite: it opens registration, with a
        // line saying why.
        toast(latest.current.t('goldenPost'));
        latest.current.navigate('/registration');
    }, []);

    return (
        <AuthRoomContext.Provider value={registry}>
            {/*
              Before the routes in the markup, so the screens' cards — later
              and positioned too — paint over it without a z-index.
            */}
            {alive && (
                <div data-auth-room hidden={!wanted}>
                    {/* The room's darkness, there at once; the video comes over it. */}
                    <div aria-hidden className="pointer-events-none fixed inset-0 bg-[#120e0b]" />
                    <Suspense fallback={null}>
                        <RoomVideo onGolden={onGolden} torn={torn} />
                    </Suspense>
                </div>
            )}
            {children}
        </AuthRoomContext.Provider>
    );
};

/**
 * Ask for the reading room for as long as the calling screen is mounted, with
 * the book whole or torn. Outside an AuthRoomProvider it does nothing: the
 * screen renders, just without the room behind it.
 */
export function useAuthRoom(torn: boolean): void {
    const room = useContext(AuthRoomContext);
    useEffect(() => room?.enter(torn), [room, torn]);
}

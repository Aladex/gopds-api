import { useEffect, useState } from 'react';
import type { Book } from '@/api/books';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import { getBotStatus, type BotStatus } from '@/api/telegram';
import { useAuth } from '@/context/AuthContext';
import { useWebSocket } from '@/context/WebSocketContext';
import { useProfileOpener } from '@/features/profile/ProfileOpenerContext';

/** Every error code that has words of its own; anything else reads as send_failed. */
const KNOWN_ERRORS = new Set([
    'not_connected',
    'not_found',
    'too_large',
    'conversion_failed',
    'send_failed',
    'uncertain',
    'bot_blocked',
    'token_rejected',
    'in_progress',
    'busy',
    'session_expired',
    'unavailable',
    'connection_lost',
]);

/** The codes that mean the connection itself needs the reader: the profile shows how. */
const CONNECTION_ERRORS = new Set(['not_connected', 'bot_blocked', 'token_rejected']);

/**
 * useSendToTelegram is the "Telegram" button's behaviour for a whole list of
 * cards, as useSendToKindle is the Kindle one's: whether the reader's bot is
 * connected, which books are on their way, and the question asked before one
 * is sent.
 *
 * A reader without a working connection — none, a bot that may not write to
 * them, a token Telegram refused — is taken to the profile's Telegram tab,
 * which shows that state and the way out of it; nothing goes to the server.
 * The connection is read when the list opens and again on a press that finds
 * it not connected, so a bot connected in the meantime is found.
 */
export const useSendToTelegram = (enabled: boolean) => {
    const { logout } = useAuth();
    const { sendToTelegram } = useWebSocket();
    const { openProfile } = useProfileOpener();
    const { t } = useTranslation();
    const [status, setStatus] = useState<BotStatus | null>(null);
    const [sending, setSending] = useState<ReadonlySet<number>>(() => new Set());
    // The book the reader is asked about. Kept after the answer, so the
    // question does not empty while it animates shut.
    const [pending, setPending] = useState<Book | null>(null);
    const [confirmOpen, setConfirmOpen] = useState(false);

    useEffect(() => {
        if (!enabled) return;
        let live = true;
        getBotStatus().then(
            (next) => live && setStatus(next),
            // Unknown is shown as not connected; a press asks again.
            () => {},
        );
        return () => {
            live = false;
        };
    }, [enabled]);

    const connected = status?.state === 'connected';

    /** request is the card button's press: the question, or the profile. */
    const request = async (book: Book) => {
        if (sending.has(book.id)) {
            return;
        }
        let current = status;
        if (!connected) {
            try {
                current = await getBotStatus();
                setStatus(current);
            } catch {
                toast.error(t('sendToTelegram.error.unavailable'));
                return;
            }
        }
        if (current?.state !== 'connected') {
            openProfile('telegram');
            return;
        }
        setPending(book);
        setConfirmOpen(true);
    };

    const send = async (bookID: number) => {
        if (sending.has(bookID)) {
            return;
        }
        setSending((now) => new Set(now).add(bookID));
        try {
            const reply = await sendToTelegram(bookID);
            if (reply.status === 'sent') {
                toast.success(t('sendToTelegram.sent'));
                return;
            }
            const code = reply.code && KNOWN_ERRORS.has(reply.code) ? reply.code : 'send_failed';
            if (code === 'in_progress') {
                toast.info(t('sendToTelegram.error.in_progress'));
                return;
            }
            toast.error(t(`sendToTelegram.error.${code}`));
            if (code === 'session_expired') {
                // As for Kindle: only once the socket provider could not
                // renew the session. Signed out the way the Logout button
                // does it.
                logout();
                return;
            }
            if (CONNECTION_ERRORS.has(code)) {
                // Changed somewhere else since this page loaded: show the
                // reader where it is put right.
                setStatus(null);
                openProfile('telegram');
            }
        } finally {
            setSending((now) => {
                const next = new Set(now);
                next.delete(bookID);
                return next;
            });
        }
    };

    return {
        connected,
        /** The bot and the format: said in the question, so they are checked once more. */
        botUsername: status?.bot_username ?? '',
        format: status?.default_format ?? 'epub',
        isSending: (bookID: number) => sending.has(bookID),
        request: (book: Book) => void request(book),
        pending,
        confirmOpen,
        /** Sends the book asked about; only the confirmation calls this. */
        confirm: () => {
            if (!pending || !confirmOpen) return;
            setConfirmOpen(false);
            void send(pending.id);
        },
        cancel: () => setConfirmOpen(false),
    };
};

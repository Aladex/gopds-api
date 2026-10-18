import { useState } from 'react';
import { useNavigate } from 'react-router';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import { useAuth } from '@/context/AuthContext';
import { useWebSocket } from '@/context/WebSocketContext';

/** Every error code that has words of its own; anything else reads as send_failed. */
const KNOWN_ERRORS = new Set([
    'not_configured',
    'not_found',
    'too_large',
    'conversion_failed',
    'send_failed',
    'in_progress',
    'unavailable',
    'connection_lost',
    'busy',
    'session_expired',
]);

/**
 * useSendToKindle is the "To Kindle" button's behaviour for a whole list of
 * cards: whether the reader has a Kindle to send to, which books are on their
 * way, and the setup dialog for a reader who has not set one up yet.
 *
 * A reader with no Kindle address gets the dialog and nothing goes to the
 * server: there is nothing it could do with the request but refuse it.
 */
export const useSendToKindle = () => {
    const { user } = useAuth();
    const { sendToKindle } = useWebSocket();
    const { t } = useTranslation();
    const navigate = useNavigate();
    const [sending, setSending] = useState<ReadonlySet<number>>(() => new Set());
    const [setupOpen, setSetupOpen] = useState(false);

    const configured = Boolean(user?.kindle_email);

    const send = async (bookID: number) => {
        if (!configured) {
            setSetupOpen(true);
            return;
        }
        if (sending.has(bookID)) {
            return;
        }
        setSending((current) => new Set(current).add(bookID));
        try {
            const reply = await sendToKindle(bookID);
            if (reply.status === 'sent') {
                // Sent, never delivered: Amazon drops a message from a sender
                // that is not approved without a word to anyone, so the hint
                // says where to look if the book does not turn up.
                toast.success(t('sendToKindle.sent'), {
                    description: t('sendToKindle.sentHint'),
                    duration: 8000,
                });
                return;
            }
            const code = reply.code && KNOWN_ERRORS.has(reply.code) ? reply.code : 'send_failed';
            if (code === 'in_progress') {
                toast.info(t('sendToKindle.error.in_progress'));
                return;
            }
            if (code === 'session_expired') {
                // The socket outlived the session; nothing is sent until the
                // reader signs in again, so say how.
                toast.error(t('sendToKindle.error.session_expired'), {
                    action: { label: t('sendToKindle.signIn'), onClick: () => navigate('/login') },
                });
                return;
            }
            toast.error(t(`sendToKindle.error.${code}`));
            if (code === 'not_configured') {
                // The address was removed somewhere else since this page
                // loaded: show the reader how to put it back.
                setSetupOpen(true);
            }
        } finally {
            setSending((current) => {
                const next = new Set(current);
                next.delete(bookID);
                return next;
            });
        }
    };

    return {
        configured,
        isSending: (bookID: number) => sending.has(bookID),
        send,
        setupOpen,
        closeSetup: () => setSetupOpen(false),
    };
};

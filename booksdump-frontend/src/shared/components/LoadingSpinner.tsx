import React from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';

import { cn } from '@/shared/lib/utils';

interface LoadingSpinnerProps {
    /** A translation key, not a finished string. */
    message?: string;
    className?: string;
}

/**
 * LoadingSpinner fills the space where something is still arriving.
 *
 * The minimum height keeps the page from collapsing and springing back as the
 * content lands. role="status" so the wait is announced rather than being a
 * silent pause.
 */
const LoadingSpinner: React.FC<LoadingSpinnerProps> = ({ message, className }) => {
    const { t } = useTranslation();

    return (
        <div
            role="status"
            className={cn(
                'flex min-h-[200px] flex-col items-center justify-center gap-3',
                className,
            )}
        >
            <Loader2 aria-hidden="true" className="size-8 animate-spin text-muted-foreground" />
            {message && <p className="text-sm text-muted-foreground">{t(message)}</p>}
        </div>
    );
};

export default LoadingSpinner;

import React from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { ArrowLeft, Loader2, type LucideIcon } from 'lucide-react';

import { Alert, AlertDescription } from '@/shared/ui/alert';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';

/**
 * The scaffolding four of the five authentication screens share: one card, one
 * real form, a heading, a message when the server refuses, and a row with a way
 * back on the left and the submit button on the right.
 *
 * It is a form and not a div because that is the only thing a password manager
 * recognises. Without it the browser has nothing to offer to save on
 * registration, nothing to fill in on sign-in, and nothing to update when the
 * password changes — which was the state of every screen here before.
 *
 * Activation stays out of this: it has no form at all, only a token from a URL.
 */

type AuthFieldProps = {
    id: string;
    label: string;
    icon: LucideIcon;
    value: string;
    onChange: (value: string) => void;
    /**
     * The token the browser matches against its saved credentials. It is
     * required rather than optional because a field that forgets it is exactly
     * the defect this scaffolding exists to prevent.
     */
    autoComplete: string;
    type?: string;
    name?: string;
};

export const AuthField: React.FC<AuthFieldProps> = ({
    id,
    label,
    icon: Icon,
    value,
    onChange,
    autoComplete,
    type = 'text',
    name,
}) => (
    <div className="flex flex-col gap-1.5">
        <label htmlFor={id} className="text-xs text-muted-foreground">
            {label}
        </label>
        <div className="relative">
            <Icon
                aria-hidden="true"
                className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground"
            />
            <Input
                id={id}
                name={name ?? id}
                type={type}
                autoComplete={autoComplete}
                value={value}
                onChange={(event) => onChange(event.target.value)}
                className="pl-9"
            />
        </div>
    </div>
);

/**
 * BackToLogin is the small arrow every screen but the sign-in one carries. It
 * is an icon button rather than a link because it is a way out of a dead end,
 * not a destination worth its own place in the tab order's reading.
 */
export const BackToLogin: React.FC = () => {
    const { t } = useTranslation();
    const navigate = useNavigate();

    return (
        <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            onClick={() => navigate('/login')}
            aria-label={t('BackButton')}
            title={t('BackButton')}
        >
            <ArrowLeft className="size-4" />
        </Button>
    );
};

type AuthFormProps = React.PropsWithChildren<{
    title: string;
    /** Empty when there is nothing to report; the alert announces itself. */
    error?: string;
    onSubmit: (event: React.FormEvent<HTMLFormElement>) => void;
    submitLabel: string;
    submitDisabled?: boolean;
    busy?: boolean;
    /** Sits opposite the submit button: a way back, or a way to get help. */
    secondaryAction?: React.ReactNode;
}>;

export const AuthForm: React.FC<AuthFormProps> = ({
    title,
    error,
    onSubmit,
    submitLabel,
    submitDisabled,
    busy,
    secondaryAction,
    children,
}) => (
    <form onSubmit={onSubmit} className="flex flex-col gap-4">
        <h1 className="text-center text-lg font-semibold">{title}</h1>

        {error && (
            <Alert variant="destructive">
                <AlertDescription>{error}</AlertDescription>
            </Alert>
        )}

        {children}

        <div className="flex items-center justify-between pt-2">
            {secondaryAction ?? <span />}
            <Button type="submit" size="sm" disabled={submitDisabled}>
                {busy && <Loader2 aria-hidden="true" className="size-4 animate-spin" />}
                {submitLabel}
            </Button>
        </div>
    </form>
);

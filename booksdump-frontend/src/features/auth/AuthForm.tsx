import React from 'react';
import { useNavigate } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { AlertCircle, ArrowLeft, type LucideIcon } from 'lucide-react';

import { Button } from '@/shared/ui/button';
import { BouncingDots } from '@/shared/ui/bouncing-dots';
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
 * BackToLogin is the way out of every screen but the sign-in one. It says so in
 * words: an unlabelled arrow asks the reader to guess where it goes, and the
 * translation for it was already written.
 */
export const BackToLogin: React.FC = () => {
    const { t } = useTranslation();
    const navigate = useNavigate();

    return (
        <Button
            type="button"
            variant="link"
            size="sm"
            onClick={() => navigate('/login')}
            className="h-auto gap-1 p-0 text-xs text-muted-foreground hover:text-foreground"
        >
            <ArrowLeft aria-hidden="true" className="size-3.5" />
            {t('BackButton')}
        </Button>
    );
};

type AuthFormProps = React.PropsWithChildren<{
    title: string;
    /** A second line saying something the title and the button do not. */
    subtitle?: string;
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
    subtitle,
    error,
    onSubmit,
    submitLabel,
    submitDisabled,
    busy,
    secondaryAction,
    children,
}) => (
    <form onSubmit={onSubmit} className="flex flex-col gap-4">
        <div>
            <h1 className="text-xl font-semibold tracking-tight">{title}</h1>
            {subtitle && <p className="mt-1 text-sm text-muted-foreground">{subtitle}</p>}
        </div>

        <div className="flex flex-col gap-3">{children}</div>

        <div className="flex flex-col gap-3">
            {/*
              A line, not a panel: the message is one short sentence, and a
              bordered box around it drew more attention to the frame than to the
              words. It sits by the button rather than above the fields, so
              appearing pushes the button down and leaves the fields where the
              reader's hands already are.
            */}
            {error && (
                <p role="alert" className="flex items-start gap-2 text-sm text-destructive">
                    <AlertCircle aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
                    {error}
                </p>
            )}

            <Button type="submit" className="w-full" disabled={submitDisabled}>
                {busy && <BouncingDots />}
                {submitLabel}
            </Button>
            {secondaryAction && <div className="flex justify-center">{secondaryAction}</div>}
        </div>
    </form>
);

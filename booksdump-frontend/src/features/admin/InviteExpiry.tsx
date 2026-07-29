import React from 'react';
import { useTranslation } from 'react-i18next';
import { format } from 'date-fns';
import { enGB, ru } from 'date-fns/locale';
import { CalendarDays } from 'lucide-react';

import { Button } from '@/shared/ui/button';
import { Calendar } from '@/shared/ui/calendar';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/shared/ui/dialog';
import { Drawer, DrawerContent, DrawerDescription, DrawerTitle } from '@/shared/ui/drawer';
import { cn } from '@/shared/lib/utils';

/** Midnight today, the earliest expiry worth offering. */
export const startOfToday = (): Date => {
    const now = new Date();
    now.setHours(0, 0, 0, 0);
    return now;
};

export const hasExpired = (iso: string): boolean => {
    const date = new Date(iso);
    return !Number.isNaN(date.getTime()) && date.getTime() <= Date.now();
};

type InviteExpiryProps = {
    /** The expiry currently held in the field, as an ISO instant. */
    value: string;
    onChange: (iso: string) => void;
    /** The shell the grid opens in; the page decides. */
    isMobile: boolean;
    disabled?: boolean;
    label: string;
    className?: string;
};

/**
 * Picks the day an invite stops working.
 *
 * This replaced the browser's own datetime field, which was chosen originally
 * for being localised and keyboard operable — both still true, and both beaten
 * here by one thing: it opens on the month it holds. An invite that expired in
 * 2025 and was forgotten made the admin page back through every month between
 * then and now before they could reach a date worth setting. This one opens on
 * the current month whatever it holds, and will not offer a day in the past,
 * because an expiry behind us is not a value anybody means to choose.
 *
 * A window rather than a popover, matching the language switcher: a grid of
 * forty-odd targets hanging off a button in a table row is a small thing to aim
 * at, and on a phone it had nowhere to hang.
 *
 * The time of day is carried over from whatever was there. Only the day is
 * being chosen, and silently moving an invite's hour is not this control's
 * business.
 */
const InviteExpiry: React.FC<InviteExpiryProps> = ({
    value,
    onChange,
    isMobile,
    disabled,
    label,
    className,
}) => {
    const { t, i18n } = useTranslation();
    const [open, setOpen] = React.useState(false);

    const locale = i18n.language === 'ru' ? ru : enGB;
    const current = new Date(value);
    const valid = !Number.isNaN(current.getTime());
    const today = startOfToday();

    // Where the grid opens. A date in the past would drag the calendar back
    // with it, which is the whole complaint.
    const month = valid && current >= today ? current : today;

    const pick = (day: Date | undefined) => {
        if (!day) {
            return;
        }
        const next = new Date(day);
        if (valid) {
            next.setHours(current.getHours(), current.getMinutes(), 0, 0);
        } else {
            // Nothing to carry over: the end of the chosen day.
            next.setHours(23, 59, 0, 0);
        }
        onChange(next.toISOString());
        setOpen(false);
    };

    const grid = (
        <Calendar
            mode="single"
            autoFocus
            locale={locale}
            selected={valid ? current : undefined}
            defaultMonth={month}
            startMonth={today}
            disabled={{ before: today }}
            onSelect={pick}
            className="mx-auto"
        />
    );

    const heading = t('beforeDate');

    return (
        <>
            <Button
                type="button"
                variant="outline"
                disabled={disabled}
                aria-label={label}
                onClick={() => setOpen(true)}
                className={cn('justify-start gap-2 font-normal', className)}
            >
                <CalendarDays className="size-4 shrink-0 opacity-70" />
                <span className="truncate">
                    {valid ? format(current, 'PPp', { locale }) : t('pickADate', 'Pick a date')}
                </span>
            </Button>

            {isMobile ? (
                <Drawer open={open} onOpenChange={setOpen}>
                    <DrawerContent>
                        <DrawerTitle className="px-4 pb-1 text-base font-medium">
                            {heading}
                        </DrawerTitle>
                        <DrawerDescription className="sr-only">{label}</DrawerDescription>
                        <div className="pb-6">{grid}</div>
                    </DrawerContent>
                </Drawer>
            ) : (
                <Dialog open={open} onOpenChange={setOpen}>
                    <DialogContent closeLabel={t('close')} className="sm:max-w-fit">
                        <DialogTitle>{heading}</DialogTitle>
                        <DialogDescription className="sr-only">{label}</DialogDescription>
                        {grid}
                    </DialogContent>
                </Dialog>
            )}
        </>
    );
};

export default InviteExpiry;

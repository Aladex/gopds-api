import React, { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import ResponsiveSheet from '@/shared/components/ResponsiveSheet';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';
import { formatCount } from '@/shared/lib/formatCount';
import { clampPage } from '@/features/catalogue/paginationRange';

type JumpFormProps = {
    fieldRef: React.Ref<HTMLInputElement>;
    onSubmit: (raw: string) => void;
};

/**
 * Its own component so the field starts empty on every opening: the sheet
 * unmounts what it holds when it closes. A number left over from the last jump
 * would be one more thing to clear on a phone keyboard.
 */
const JumpForm: React.FC<JumpFormProps> = ({ fieldRef, onSubmit }) => {
    const { t } = useTranslation();
    const [value, setValue] = useState('');

    return (
        <form
            className="flex gap-2 max-[600px]:pb-5"
            onSubmit={(event) => {
                event.preventDefault();
                onSubmit(value);
            }}
        >
            {/*
              Text with a numeric keyboard, not type="number": that one has
              spinners, takes `e`, and reports an empty value for anything it
              cannot parse, so a bad paste could not be cleaned up here.
            */}
            <Input
                ref={fieldRef}
                type="text"
                inputMode="numeric"
                enterKeyHint="go"
                autoComplete="off"
                aria-label={t('pager.jumpPlaceholder')}
                placeholder={t('pager.jumpPlaceholder')}
                value={value}
                onChange={(event) => setValue(event.target.value)}
                className="max-[600px]:h-11"
            />
            <Button type="submit" className="max-[600px]:h-11">
                {t('pager.jumpGo')}
            </Button>
        </form>
    );
};

type JumpSheetProps = {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    currentPage: number;
    totalPages: number;
    onJump: (page: number) => void;
};

/**
 * JumpSheet takes a page number. It is the way to a far page on a phone, where
 * the numbers are gone, and behind the ellipsis on a wider screen, where they
 * stand for what the window leaves out.
 *
 * It opens over the list where the reader stands; only going somewhere moves
 * the page.
 */
const JumpSheet: React.FC<JumpSheetProps> = ({
    open,
    onOpenChange,
    currentPage,
    totalPages,
    onJump,
}) => {
    const { t, i18n } = useTranslation();
    const field = useRef<HTMLInputElement>(null);
    const opener = useRef<HTMLElement | null>(null);

    const submit = (raw: string) => {
        const page = clampPage(raw, totalPages);
        if (page === null) {
            return;
        }
        onOpenChange(false);
        if (page !== currentPage) {
            onJump(page);
        }
    };

    return (
        <ResponsiveSheet
            open={open}
            onOpenChange={onOpenChange}
            title={t('pager.jumpTitle')}
            description={t('pager.jumpRange', {
                page: formatCount(currentPage, i18n.language),
                total: formatCount(totalPages, i18n.language),
            })}
            closeLabel={t('close')}
            dialogClassName="sm:max-w-sm"
            onOpenAutoFocus={(event) => {
                // The field and not the first button: a number is all this asks.
                event.preventDefault();
                opener.current = document.activeElement as HTMLElement | null;
                field.current?.focus();
            }}
            onCloseAutoFocus={(event) => {
                // No Radix trigger to go back to, so the sheet would leave focus
                // on <body>. Not after a jump: the list has gone with the opener.
                event.preventDefault();
                if (opener.current && document.contains(opener.current)) {
                    opener.current.focus({ preventScroll: true });
                }
            }}
        >
            <JumpForm fieldRef={field} onSubmit={submit} />
        </ResponsiveSheet>
    );
};

export default JumpSheet;

import React from 'react';

import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogTitle,
} from '@/shared/ui/dialog';
import {
    Drawer,
    DrawerContent,
    DrawerDescription,
    DrawerFooter,
    DrawerTitle,
} from '@/shared/ui/drawer';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { cn } from '@/shared/lib/utils';

/** The breakpoint every sheet/dialog pair in the app splits at. */
export const SHEET_QUERY = '(max-width: 600px)';

type ResponsiveSheetProps = {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    title: React.ReactNode;
    /** The first part of the body: scrolls with it, and names the surface. */
    description: React.ReactNode;
    /** The rest of the body. */
    children?: React.ReactNode;
    /** The main action, and the way out. */
    primary: React.ReactNode;
    secondary: React.ReactNode;
    /** The dialog close button's accessible name. */
    closeLabel: string;
    /** Width cap of the desktop dialog. */
    dialogClassName?: string;
    onOpenAutoFocus?: (event: Event) => void;
    onCloseAutoFocus?: (event: Event) => void;
    'data-testid'?: string;
};

/**
 * ResponsiveSheet is the app's dialog-or-sheet: a dialog on a desktop, a sheet
 * from the bottom edge on a phone, split where every such pair splits.
 *
 * Three parts, and only the middle one scrolls. The title stays above it and
 * the actions stay below it, so however long the body runs — a collection
 * with twenty authors, a video and its steps — the way on and the way out are
 * always on screen. A body left to grow pushed them off a 640px-high phone,
 * where nothing could scroll them back.
 *
 * The actions are given as the main one and the way out, and placed as the
 * app places them: stacked and full width with the main one first on a phone,
 * in a row with the main one last — rightmost — on a desktop.
 */
const ResponsiveSheet: React.FC<ResponsiveSheetProps> = ({
    open,
    onOpenChange,
    title,
    description,
    children,
    primary,
    secondary,
    closeLabel,
    dialogClassName,
    onOpenAutoFocus,
    onCloseAutoFocus,
    'data-testid': testID,
}) => {
    const isMobile = useMediaQuery(SHEET_QUERY);
    const scroller = testID ? `${testID}-scroll` : undefined;
    const footer = testID ? `${testID}-footer` : undefined;

    if (isMobile) {
        return (
            <Drawer open={open} onOpenChange={onOpenChange}>
                {/* The height in the primitive's own variant form, as the
                    profile sheet has it: a plain max-h loses to the bottom
                    sheet's own cap on specificity. */}
                <DrawerContent
                    data-testid={testID}
                    className="data-[vaul-drawer-direction=bottom]:max-h-[92vh]"
                    onOpenAutoFocus={onOpenAutoFocus}
                    onCloseAutoFocus={onCloseAutoFocus}
                >
                    <DrawerTitle className="px-4 pb-1 text-base font-medium">{title}</DrawerTitle>
                    <div
                        data-testid={scroller}
                        className="scrollbar-thin flex min-h-0 flex-col gap-4 overflow-y-auto px-4"
                    >
                        <DrawerDescription asChild>
                            <div className="text-sm text-muted-foreground">{description}</div>
                        </DrawerDescription>
                        {children}
                    </div>
                    <DrawerFooter data-testid={footer} className="pt-3">
                        {primary}
                        {secondary}
                    </DrawerFooter>
                </DrawerContent>
            </Drawer>
        );
    }

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            {/* A column rather than the primitive's grid, capped at 85vh as
                the profile and the donate dialog are: the body takes what the
                title and the actions leave and scrolls inside it. */}
            <DialogContent
                data-testid={testID}
                closeLabel={closeLabel}
                className={cn('flex max-h-[85vh] flex-col', dialogClassName)}
                onOpenAutoFocus={onOpenAutoFocus}
                onCloseAutoFocus={onCloseAutoFocus}
            >
                {/* Clear of the close button in the corner. */}
                <DialogTitle className="pr-8">{title}</DialogTitle>
                <div
                    data-testid={scroller}
                    className="scrollbar-thin -mx-1 flex min-h-0 flex-col gap-4 overflow-y-auto px-1"
                >
                    <DialogDescription asChild>
                        <div>{description}</div>
                    </DialogDescription>
                    {children}
                </div>
                <DialogFooter data-testid={footer}>
                    {secondary}
                    {primary}
                </DialogFooter>
            </DialogContent>
        </Dialog>
    );
};

export default ResponsiveSheet;

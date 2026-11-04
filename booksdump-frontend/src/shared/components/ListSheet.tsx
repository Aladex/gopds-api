import React from 'react';

import { Drawer, DrawerContent, DrawerDescription, DrawerTitle } from '@/shared/ui/drawer';

/**
 * ListSheet is the bottom sheet a phone opens for a short list of choices:
 * which format to download, where to send a book, which of a book's authors
 * to open.
 *
 * It is the language list's sheet (LanguageSwitcher) made reusable, not a
 * new one: the same Drawer with its handle, the title in the same place and
 * size, the same side padding, and a body that scrolls under the title while
 * the sheet keeps the primitive's own height cap. The title and description
 * do not shrink: a list of forty-six authors otherwise squeezed them out. The description says which
 * book the list is about, as a line under the title.
 */

/**
 * One row of such a list, as the language list draws its rows: a finger's
 * height, the full width of the sheet, the accent on hover and the focus ring
 * every control in the app has. Disabled rows keep their look and drop the
 * hover, so a row that is busy does not invite a press.
 */
export const LIST_SHEET_ROW_CLASS = [
    'flex min-h-12 w-full items-center gap-3 rounded px-4 text-left text-base',
    'hover:bg-accent',
    'outline-none focus-visible:ring-[3px] focus-visible:ring-focus',
    'disabled:cursor-default disabled:hover:bg-transparent',
].join(' ');

type ListSheetProps = React.PropsWithChildren<{
    open: boolean;
    onOpenChange: (open: boolean) => void;
    title: React.ReactNode;
    description: React.ReactNode;
    'data-testid'?: string;
}>;

const ListSheet: React.FC<ListSheetProps> = ({
    open,
    onOpenChange,
    title,
    description,
    children,
    'data-testid': testID,
}) => (
    <Drawer open={open} onOpenChange={onOpenChange}>
        <DrawerContent data-testid={testID}>
            <DrawerTitle className="shrink-0 px-4 pb-1 text-base font-medium">{title}</DrawerTitle>
            <DrawerDescription className="shrink-0 truncate px-4 pb-2 text-sm text-muted-foreground">
                {description}
            </DrawerDescription>
            <div className="scrollbar-thin min-h-0 overflow-y-auto pb-6">{children}</div>
        </DrawerContent>
    </Drawer>
);

export default ListSheet;

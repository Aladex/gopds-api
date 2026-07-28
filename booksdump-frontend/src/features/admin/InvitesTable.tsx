import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Plus, Trash2 } from 'lucide-react';

import * as adminApi from '@/api/admin';
import { Button } from '@/shared/ui/button';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/shared/ui/dialog';
import { Input } from '@/shared/ui/input';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/shared/ui/table';

interface Invite {
    id?: string;
    invite: string;
    before_date: string;
}

/**
 * The expiry is edited with the browser's own datetime field.
 *
 * It replaces a date picker that carried @mui/x-date-pickers and a date-fns
 * adapter for this one input on this one screen. The native control is already
 * localised, already keyboard operable, and on a phone it is the platform's own
 * wheel rather than a calendar grid drawn at desktop proportions.
 */

/**
 * toLocalInput renders an instant the way datetime-local wants it: local wall
 * clock, to the minute, with no zone. toISOString would give UTC and the field
 * would silently show the wrong hour to anyone east or west of it.
 */
const toLocalInput = (iso: string): string => {
    const date = new Date(iso);
    if (Number.isNaN(date.getTime())) {
        return '';
    }
    const pad = (value: number) => String(value).padStart(2, '0');
    return (
        `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}` +
        `T${pad(date.getHours())}:${pad(date.getMinutes())}`
    );
};

/** A month from now, which is what a new invite is worth by default. */
const defaultExpiry = () => {
    const date = new Date();
    date.setMonth(date.getMonth() + 1);
    return date.toISOString();
};

const InvitesTable: React.FC = () => {
    const { t } = useTranslation();
    const [invites, setInvites] = useState<Invite[]>([]);
    const [dialogOpen, setDialogOpen] = useState(false);
    const [newInviteCode, setNewInviteCode] = useState('');

    useEffect(() => {
        const fetchInvites = async () => {
            try {
                const data = await adminApi.listInvites<Invite>();
                setInvites(data.result);
            } catch (error) {
                console.error(error);
            }
        };

        fetchInvites();
    }, []);

    const handleInviteChange = async (invite: Invite) => {
        try {
            await adminApi.changeInvite('update', invite);
            setInvites(invites.map((inv) => (inv.id === invite.id ? invite : inv)));
        } catch (error) {
            console.error(error);
        }
    };

    const handleDeleteInvite = async (invite: Invite) => {
        try {
            await adminApi.changeInvite('delete', invite);
            setInvites(invites.filter((inv) => inv.id !== invite.id));
        } catch (error) {
            console.error(error);
        }
    };

    const handleDateChange = (invite: Invite) => (value: string) => {
        // An empty or half-typed field is not a date; leave the invite alone
        // rather than sending the server an Invalid Date.
        const date = new Date(value);
        if (!value || Number.isNaN(date.getTime())) {
            return;
        }
        handleInviteChange({ ...invite, before_date: date.toISOString() });
    };

    const handleAddInvite = async () => {
        try {
            await adminApi.changeInvite('create', {
                invite: newInviteCode,
                before_date: defaultExpiry(),
            });

            const data = await adminApi.listInvites<Invite>();
            setInvites(data.result);
            setDialogOpen(false);
            setNewInviteCode('');
        } catch (error) {
            console.error(error);
        }
    };

    return (
        <div className="flex flex-col gap-3">
            <div className="flex items-center justify-between">
                <h2 className="text-base font-medium">{t('invites')}</h2>
                <Button
                    variant="outline"
                    size="icon-sm"
                    onClick={() => setDialogOpen(true)}
                    title={t('addInvite')}
                    aria-label={t('addInvite')}
                >
                    <Plus className="size-4" />
                </Button>
            </div>

            <div className="rounded border border-border bg-card">
                <Table>
                    <TableHeader>
                        <TableRow>
                            <TableHead>{t('inviteId')}</TableHead>
                            <TableHead>{t('inviteCode')}</TableHead>
                            <TableHead>{t('beforeDate')}</TableHead>
                            <TableHead>{t('actions')}</TableHead>
                        </TableRow>
                    </TableHeader>
                    <TableBody>
                        {invites.map((invite) => (
                            <TableRow key={invite.id}>
                                <TableCell className="tabular-nums text-muted-foreground">
                                    {invite.id}
                                </TableCell>
                                <TableCell className="font-medium">{invite.invite}</TableCell>
                                <TableCell>
                                    <Input
                                        type="datetime-local"
                                        aria-label={`${t('beforeDate')}: ${invite.invite}`}
                                        value={toLocalInput(invite.before_date)}
                                        onChange={(event) =>
                                            handleDateChange(invite)(event.target.value)
                                        }
                                        // The picker glyph is drawn black by the
                                        // browser, invisible on a dark field.
                                        className="w-auto min-w-[13rem] dark:[&::-webkit-calendar-picker-indicator]:invert"
                                    />
                                </TableCell>
                                <TableCell className="w-12">
                                    <Button
                                        variant="ghost"
                                        size="icon-sm"
                                        onClick={() => handleDeleteInvite(invite)}
                                        title={t('delete')}
                                        aria-label={`${t('delete')}: ${invite.invite}`}
                                    >
                                        <Trash2 className="size-4" />
                                    </Button>
                                </TableCell>
                            </TableRow>
                        ))}
                        {invites.length === 0 && (
                            <TableRow>
                                <TableCell
                                    colSpan={4}
                                    className="py-6 text-center text-muted-foreground"
                                >
                                    {t('noInvites', 'No invites yet')}
                                </TableCell>
                            </TableRow>
                        )}
                    </TableBody>
                </Table>
            </div>

            <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
                <DialogContent closeLabel={t('close')}>
                    <DialogHeader>
                        <DialogTitle>{t('addInvite')}</DialogTitle>
                        <DialogDescription className="sr-only">{t('addInvite')}</DialogDescription>
                    </DialogHeader>

                    <Input
                        autoFocus
                        aria-label={t('inviteCode')}
                        placeholder={t('inviteCode')}
                        value={newInviteCode}
                        onChange={(event) => setNewInviteCode(event.target.value)}
                        onKeyDown={(event) => {
                            if (event.key === 'Enter' && newInviteCode.trim()) {
                                handleAddInvite();
                            }
                        }}
                    />

                    <DialogFooter>
                        <Button variant="ghost" onClick={() => setDialogOpen(false)}>
                            {t('cancel')}
                        </Button>
                        <Button onClick={handleAddInvite} disabled={!newInviteCode.trim()}>
                            {t('addInvite')}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
};

export default InvitesTable;

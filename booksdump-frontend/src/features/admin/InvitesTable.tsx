import React, { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { format } from 'date-fns';
import { enGB, ru } from 'date-fns/locale';
import { Check, Plus, Trash2 } from 'lucide-react';
import { toast } from 'sonner';

import * as adminApi from '@/api/admin';
import { Badge } from '@/shared/ui/badge';
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
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import InviteExpiry, { hasExpired } from '@/features/admin/InviteExpiry';

interface Invite {
    id?: string;
    invite: string;
    before_date: string;
}

/** A month from now, which is what a new invite is worth by default. */
const defaultExpiry = () => {
    const date = new Date();
    date.setMonth(date.getMonth() + 1);
    return date.toISOString();
};

const InvitesTable: React.FC = () => {
    const { t, i18n } = useTranslation();
    const [invites, setInvites] = useState<Invite[]>([]);
    const [dialogOpen, setDialogOpen] = useState(false);
    const [newInviteCode, setNewInviteCode] = useState('');
    const [pendingDelete, setPendingDelete] = useState<Invite | null>(null);
    const [saving, setSaving] = useState<string | null>(null);

    // Edits are held here until saved. The field used to send a request on
    // every keystroke, so a half-typed year reached the server and the admin
    // was never told whether any of it landed.
    const [drafts, setDrafts] = useState<Record<string, string>>({});

    const locale = i18n.language === 'ru' ? ru : enGB;

    // Below this the four-column table stops fitting and each invite becomes a
    // card, the same width the users table changes at.
    const isMobile = useMediaQuery('(max-width: 899px)');

    const load = async () => {
        try {
            const data = await adminApi.listInvites<Invite>();
            setInvites(data.result);
        } catch (error) {
            console.error(error);
            toast.error(t('invitesLoadError', 'Could not load invites'));
        }
    };

    useEffect(() => {
        load();
        // Loaded once when the panel opens; the list is small and changes only
        // through this screen.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    const draftFor = (invite: Invite) => drafts[invite.id ?? ''] ?? invite.before_date;
    const isDirty = (invite: Invite) => draftFor(invite) !== invite.before_date;

    const setDraft = (invite: Invite) => (iso: string) => {
        if (!invite.id) {
            return;
        }
        setDrafts((current) => ({ ...current, [invite.id as string]: iso }));
    };

    const discardDraft = (invite: Invite) => {
        setDrafts((current) => {
            const next = { ...current };
            delete next[invite.id ?? ''];
            return next;
        });
    };

    const saveExpiry = async (invite: Invite) => {
        const updated = { ...invite, before_date: draftFor(invite) };
        setSaving(invite.id ?? null);
        try {
            await adminApi.changeInvite('update', updated);
            setInvites((current) => current.map((one) => (one.id === invite.id ? updated : one)));
            discardDraft(invite);
            toast.success(t('inviteSaved', { code: invite.invite }));
        } catch (error) {
            console.error(error);
            toast.error(t('inviteSaveError', { code: invite.invite }));
        } finally {
            setSaving(null);
        }
    };

    const confirmDelete = async () => {
        if (!pendingDelete) {
            return;
        }
        const invite = pendingDelete;
        try {
            await adminApi.changeInvite('delete', invite);
            setInvites((current) => current.filter((one) => one.id !== invite.id));
            discardDraft(invite);
            toast.success(t('inviteDeleted', { code: invite.invite }));
        } catch (error) {
            console.error(error);
            toast.error(t('inviteDeleteError', { code: invite.invite }));
        } finally {
            setPendingDelete(null);
        }
    };

    const handleAddInvite = async () => {
        const code = newInviteCode.trim();
        if (!code) {
            return;
        }
        try {
            await adminApi.changeInvite('create', { invite: code, before_date: defaultExpiry() });
            await load();
            setDialogOpen(false);
            setNewInviteCode('');
            toast.success(t('inviteCreated', { code }));
        } catch (error) {
            console.error(error);
            toast.error(t('inviteCreateError', { code }));
        }
    };

    /** Says at a glance whether an invite is still worth handing out. */
    const ExpiryBadge: React.FC<{ invite: Invite }> = ({ invite }) =>
        hasExpired(invite.before_date) ? (
            <Badge variant="destructive">{t('inviteExpired', 'Expired')}</Badge>
        ) : null;

    const SaveButton: React.FC<{ invite: Invite }> = ({ invite }) =>
        isDirty(invite) ? (
            <Button
                size="sm"
                disabled={saving === invite.id}
                onClick={() => saveExpiry(invite)}
                aria-label={`${t('save')}: ${invite.invite}`}
            >
                <Check className="size-4" />
                {t('save')}
            </Button>
        ) : null;

    const DeleteButton: React.FC<{ invite: Invite }> = ({ invite }) => (
        <Button
            variant="ghost"
            size="icon-sm"
            onClick={() => setPendingDelete(invite)}
            title={t('delete')}
            aria-label={`${t('delete')}: ${invite.invite}`}
        >
            <Trash2 className="size-4" />
        </Button>
    );

    const empty = (
        <div className="rounded border border-border bg-card p-6 text-center text-muted-foreground">
            {t('noInvites', 'No invites yet')}
        </div>
    );

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

            {invites.length === 0 ? (
                empty
            ) : isMobile ? (
                <div className="flex flex-col gap-3">
                    {invites.map((invite) => (
                        <div
                            key={invite.id}
                            className="flex flex-col gap-3 rounded border border-border bg-card p-4"
                        >
                            <div className="flex min-w-0 items-start justify-between gap-2">
                                <div className="flex min-w-0 flex-col gap-1">
                                    <span className="truncate font-medium">{invite.invite}</span>
                                    <span className="text-xs text-muted-foreground tabular-nums">
                                        {t('inviteId')}: {invite.id}
                                    </span>
                                </div>
                                <div className="flex shrink-0 items-center gap-1">
                                    <ExpiryBadge invite={invite} />
                                    <DeleteButton invite={invite} />
                                </div>
                            </div>

                            <div className="flex flex-col gap-2">
                                <span className="text-xs text-muted-foreground">
                                    {t('beforeDate')}
                                </span>
                                <InviteExpiry
                                    value={draftFor(invite)}
                                    onChange={setDraft(invite)}
                                    isMobile={isMobile}
                                    label={`${t('beforeDate')}: ${invite.invite}`}
                                    className="w-full"
                                />
                                <SaveButton invite={invite} />
                            </div>
                        </div>
                    ))}
                </div>
            ) : (
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
                                    <TableCell>
                                        <div className="flex items-center gap-2">
                                            <span className="font-medium">{invite.invite}</span>
                                            <ExpiryBadge invite={invite} />
                                        </div>
                                    </TableCell>
                                    <TableCell>
                                        <div className="flex items-center gap-2">
                                            <InviteExpiry
                                                value={draftFor(invite)}
                                                onChange={setDraft(invite)}
                                                isMobile={isMobile}
                                                label={`${t('beforeDate')}: ${invite.invite}`}
                                                className="min-w-[13rem]"
                                            />
                                            <SaveButton invite={invite} />
                                        </div>
                                    </TableCell>
                                    <TableCell className="w-12">
                                        <DeleteButton invite={invite} />
                                    </TableCell>
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                </div>
            )}

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

            {/*
              Deleting used to happen on the first click, with nothing said
              before or after. An invite is one row among many that look alike,
              and the button sits at the end of every one of them.
            */}
            <Dialog
                open={pendingDelete !== null}
                onOpenChange={(open) => !open && setPendingDelete(null)}
            >
                <DialogContent closeLabel={t('close')}>
                    <DialogHeader>
                        <DialogTitle>{t('deleteInviteTitle', 'Delete this invite?')}</DialogTitle>
                        <DialogDescription>
                            {t('deleteInviteBody', {
                                code: pendingDelete?.invite ?? '',
                                date: pendingDelete
                                    ? format(new Date(pendingDelete.before_date), 'PPp', { locale })
                                    : '',
                            })}
                        </DialogDescription>
                    </DialogHeader>
                    <DialogFooter>
                        <Button variant="ghost" onClick={() => setPendingDelete(null)}>
                            {t('cancel')}
                        </Button>
                        <Button variant="destructive" onClick={confirmDelete}>
                            {t('delete')}
                        </Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
        </div>
    );
};

export default InvitesTable;

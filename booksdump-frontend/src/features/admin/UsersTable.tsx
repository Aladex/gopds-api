import React, { useEffect, useState } from 'react';
import { useLocation, useParams } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import {
    ArrowDown,
    ArrowUp,
    ArrowUpDown,
    CalendarDays,
    Check,
    LogIn,
    Mail,
    Pencil,
    ShieldCheck,
    Trash2,
    User as UserIcon,
    X,
} from 'lucide-react';

import { Badge } from '@/shared/ui/badge';
import { Button } from '@/shared/ui/button';
import { Card, CardContent, CardFooter } from '@/shared/ui/card';
import { Checkbox } from '@/shared/ui/checkbox';
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
} from '@/shared/ui/dialog';
import { Field } from '@/shared/ui/field';
import { Input } from '@/shared/ui/input';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/shared/ui/select';
import {
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableRow,
} from '@/shared/ui/table';
import { useMediaQuery } from '@/shared/hooks/useMediaQuery';
import { formatDate } from '@/shared/lib/formatDate';
import * as adminApi from '@/api/admin';
import BookPagination from '@/features/catalogue/BookPagination';

// User interface
interface User {
    id: number;
    username: string;
    email: string;
    first_name: string;
    last_name: string;
    active: boolean;
    is_superuser: boolean;
    date_joined: string;
    last_login: string;
    token?: string;
}

// Update user payload interface
interface UpdateUserPayload extends Omit<User, 'token'> {
    bot_token: string;
    new_password?: string;
}

/** The two columns the backend will sort on. */
type SortColumn = 'date_joined' | 'last_login';

/**
 * A yes/no cell, drawn rather than written.
 *
 * MUI put a checkbox glyph here, which reads as something you could click. A
 * tick and a cross say the same thing without inviting the click, and each
 * carries its own text for a screen reader — an icon alone announces nothing.
 */
const BooleanCell: React.FC<{ value: boolean; yes: string; no: string }> = ({ value, yes, no }) =>
    value ? (
        <span className="inline-flex items-center gap-1 text-primary">
            <Check aria-hidden="true" className="size-4" />
            <span className="sr-only">{yes}</span>
        </span>
    ) : (
        <span className="inline-flex items-center gap-1 text-muted-foreground">
            <X aria-hidden="true" className="size-4" />
            <span className="sr-only">{no}</span>
        </span>
    );

/**
 * SortableHead is a column heading you can click to sort by.
 *
 * aria-sort on the cell is what tells assistive technology which column the
 * table is currently ordered by and in which direction; the arrow only tells
 * the people who can see it.
 */
const SortableHead: React.FC<{
    label: string;
    column: SortColumn;
    active: boolean;
    descending: boolean;
    onSort: (column: SortColumn) => void;
}> = ({ label, column, active, descending, onSort }) => (
    <TableHead aria-sort={active ? (descending ? 'descending' : 'ascending') : 'none'}>
        <Button
            variant="ghost"
            size="sm"
            className="-mx-2 h-auto px-2 py-1 text-xs font-medium tracking-wide uppercase"
            onClick={() => onSort(column)}
        >
            {label}
            {active ? (
                descending ? (
                    <ArrowDown aria-hidden="true" className="size-3" />
                ) : (
                    <ArrowUp aria-hidden="true" className="size-3" />
                )
            ) : (
                <ArrowUpDown aria-hidden="true" className="size-3 opacity-40" />
            )}
        </Button>
    </TableHead>
);

// UserCard component for mobile view
interface UserCardProps {
    user: User;
    onEdit: (user: User) => void;
    onDelete: (user: User) => void;
    t: (key: string) => string;
}

const UserCard: React.FC<UserCardProps> = ({ user, onEdit, onDelete, t }) => (
    <Card className="gap-3 py-4 transition-shadow hover:shadow-md">
        <CardContent className="flex flex-col gap-3">
            <div className="flex items-center justify-between gap-2">
                <div className="flex min-w-0 items-center gap-2">
                    <UserIcon aria-hidden="true" className="size-4 shrink-0 text-primary" />
                    <h3 className="truncate text-base font-medium">{user.username}</h3>
                </div>
                <Badge variant="outline" className="tabular-nums">
                    {t('userId')}: {user.id}
                </Badge>
            </div>

            <hr className="border-0 border-t border-border" />

            <div className="flex items-center gap-2 text-sm text-muted-foreground">
                <Mail aria-hidden="true" className="size-4 shrink-0" />
                <span className="truncate">{user.email}</span>
            </div>

            <div className="flex flex-wrap gap-2">
                <Badge variant={user.active ? 'default' : 'outline'}>{t('active')}</Badge>
                {user.is_superuser && (
                    <Badge variant="secondary">
                        <ShieldCheck aria-hidden="true" className="size-3" />
                        {t('superuser')}
                    </Badge>
                )}
            </div>

            <div className="flex flex-col gap-1 text-xs text-muted-foreground">
                <span className="flex items-center gap-2">
                    <CalendarDays aria-hidden="true" className="size-4 shrink-0" />
                    {t('dateJoined')}: {formatDate(user.date_joined)}
                </span>
                <span className="flex items-center gap-2">
                    <LogIn aria-hidden="true" className="size-4 shrink-0" />
                    {t('lastLogin')}: {formatDate(user.last_login)}
                </span>
            </div>
        </CardContent>

        <CardFooter className="justify-end gap-2">
            <Button
                variant="ghost"
                size="sm"
                onClick={() => onEdit(user)}
                aria-label={`${t('edit')}: ${user.username}`}
            >
                <Pencil className="size-4" />
                <span className="max-sm:sr-only">{t('edit')}</span>
            </Button>
            <Button
                variant="ghost"
                size="sm"
                className="text-destructive hover:text-destructive"
                onClick={() => onDelete(user)}
                aria-label={`${t('deleteUser')}: ${user.username}`}
            >
                <Trash2 className="size-4" />
                <span className="max-sm:sr-only">{t('deleteUser')}</span>
            </Button>
        </CardFooter>
    </Card>
);

const UsersTable: React.FC = () => {
    const { page } = useParams<{ page: string }>();
    const [users, setUsers] = useState<User[]>([]);
    const [sortOrder, setSortOrder] = useState<boolean>(false);
    const [sortColumn, setSortColumn] = useState<SortColumn>('last_login');
    const [totalPages, setTotalPages] = useState<number>(0);
    const location = useLocation();
    const { t } = useTranslation();
    const [dialogOpen, setDialogOpen] = useState<boolean>(false);
    const [selectedUser, setSelectedUser] = useState<User | null>(null);
    const [firstName, setFirstName] = useState<string>('');
    const [newPassword, setNewPassword] = useState<string>('');
    const [email, setEmail] = useState<string>('');
    const [lastName, setLastName] = useState<string>('');
    const [token, setToken] = useState<string>('');
    const [isActive, setIsActive] = useState<boolean>(false);
    const [isSuperuser, setIsSuperuser] = useState<boolean>(false);
    const [searchQuery, setSearchQuery] = useState<string>('');

    // Below this the nine-column table stops fitting and each user becomes a
    // card. It is the width MUI called `md`.
    const isMobile = useMediaQuery('(max-width: 899px)');

    useEffect(() => {
        const fetchUsers = async () => {
            const limit = 50;
            const offset = (parseInt(page || '1') - 1) * limit;
            try {
                const data = await adminApi.listUsers<User>({
                    limit,
                    offset,
                    username: searchQuery,
                    order: sortColumn,
                    desc: sortOrder,
                });
                setUsers(data.users);
                setTotalPages(data.length);
            } catch (error) {
                console.error(error);
            }
        };

        fetchUsers();
    }, [page, sortOrder, sortColumn, searchQuery]);

    const handleSortRequest = (column: SortColumn) => {
        if (sortColumn === column) {
            setSortOrder((prevOrder) => !prevOrder); // Toggle sort order
        } else {
            setSortColumn(column);
            setSortOrder(false); // Reset to asc when changing columns
        }
    };

    const handleEditClick = (user: User) => {
        setSelectedUser(user);
        setDialogOpen(true);
        setFirstName(user.first_name);
        setLastName(user.last_name);
        setEmail(user.email);
        setIsActive(user.active);
        setIsSuperuser(user.is_superuser);
        setToken(user.token ?? '');
        // A stale password from a previous edit must not follow the admin onto
        // the next user, where it would silently reset that account.
        setNewPassword('');
    };

    const handleDialogClose = () => {
        setDialogOpen(false);
        setSelectedUser(null);
    };

    const handleDeleteClick = async (user: User) => {
        try {
            await adminApi.deleteUser(user.id);
            setUsers(users.filter((u) => u.id !== user.id));
        } catch (error) {
            console.error('Error deleting user:', error);
        }
    };

    const handleUserChange = async () => {
        if (!selectedUser) return;
        const updatedUser: UpdateUserPayload = {
            ...selectedUser,
            first_name: firstName,
            last_name: lastName,
            email: email,
            active: isActive,
            is_superuser: isSuperuser,
            bot_token: token,
        };
        if (newPassword) {
            updatedUser.new_password = newPassword; // Add password only if it is set
        }
        try {
            await adminApi.changeUser('update', updatedUser);
            // Update the user in the users array
            setUsers(users.map((user) => (user.id === updatedUser.id ? updatedUser : user)));
            handleDialogClose();
        } catch (error) {
            console.error(error);
        }
    };

    return (
        <div className="flex min-h-[calc(100vh-240px)] flex-col gap-4">
            <h2 className="text-center text-lg font-medium">{t('users')}</h2>

            <Field id="users-search" label={t('search')} className="max-w-80">
                <Input
                    id="users-search"
                    value={searchQuery}
                    onChange={(event) => setSearchQuery(event.target.value)}
                />
            </Field>

            {/* Responsive rendering: Cards on mobile, Table on desktop */}
            {isMobile ? (
                <div className="flex flex-col gap-4">
                    {/* A card list has no column headings to click, so sorting
                        needs controls of its own. */}
                    <div className="flex flex-wrap items-end gap-2">
                        <Field id="users-sort-column" label={t('sortBy')} className="min-w-50 flex-1">
                            <Select
                                value={sortColumn}
                                onValueChange={(value) => setSortColumn(value as SortColumn)}
                            >
                                <SelectTrigger id="users-sort-column" className="w-full">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                    <SelectItem value="date_joined">
                                        <CalendarDays aria-hidden="true" className="size-4" />
                                        {t('dateJoined')}
                                    </SelectItem>
                                    <SelectItem value="last_login">
                                        <LogIn aria-hidden="true" className="size-4" />
                                        {t('lastLogin')}
                                    </SelectItem>
                                </SelectContent>
                            </Select>
                        </Field>

                        <Button
                            variant="outline"
                            size="icon"
                            onClick={() => setSortOrder(!sortOrder)}
                            title={sortOrder ? t('sortDescending') : t('sortAscending')}
                            aria-label={sortOrder ? t('sortDescending') : t('sortAscending')}
                        >
                            {sortOrder ? (
                                <ArrowDown className="size-4" />
                            ) : (
                                <ArrowUp className="size-4" />
                            )}
                        </Button>
                    </div>

                    {users.length === 0 ? (
                        <Card>
                            <CardContent className="text-center text-sm text-muted-foreground">
                                {t('noUsersFound')}
                            </CardContent>
                        </Card>
                    ) : (
                        <div className="flex flex-col gap-4">
                            {users.map((user) => (
                                <UserCard
                                    key={user.id}
                                    user={user}
                                    onEdit={handleEditClick}
                                    onDelete={handleDeleteClick}
                                    t={t}
                                />
                            ))}
                        </div>
                    )}
                </div>
            ) : (
                <div className="rounded border border-border bg-card">
                    <Table>
                        <TableHeader>
                            <TableRow>
                                <TableHead className="text-right">{t('userId')}</TableHead>
                                <TableHead>{t('user')}</TableHead>
                                <TableHead>{t('email')}</TableHead>
                                <TableHead>{t('active')}</TableHead>
                                <TableHead>{t('superuser')}</TableHead>
                                <SortableHead
                                    label={t('dateJoined')}
                                    column="date_joined"
                                    active={sortColumn === 'date_joined'}
                                    descending={sortOrder}
                                    onSort={handleSortRequest}
                                />
                                <SortableHead
                                    label={t('lastLogin')}
                                    column="last_login"
                                    active={sortColumn === 'last_login'}
                                    descending={sortOrder}
                                    onSort={handleSortRequest}
                                />
                                <TableHead>
                                    <span className="sr-only">{t('edit')}</span>
                                </TableHead>
                                <TableHead>
                                    <span className="sr-only">{t('deleteUser')}</span>
                                </TableHead>
                            </TableRow>
                        </TableHeader>
                        <TableBody>
                            {users.map((user) => (
                                <TableRow key={user.id}>
                                    <TableCell className="text-right tabular-nums text-muted-foreground">
                                        {user.id}
                                    </TableCell>
                                    <TableCell className="font-medium">{user.username}</TableCell>
                                    <TableCell>{user.email}</TableCell>
                                    <TableCell>
                                        <BooleanCell
                                            value={user.active}
                                            yes={t('active')}
                                            no={t('inactive', 'Inactive')}
                                        />
                                    </TableCell>
                                    <TableCell>
                                        <BooleanCell
                                            value={user.is_superuser}
                                            yes={t('superuser')}
                                            no={t('notSuperuser', 'Not a superuser')}
                                        />
                                    </TableCell>
                                    <TableCell className="whitespace-nowrap tabular-nums">
                                        {formatDate(user.date_joined)}
                                    </TableCell>
                                    <TableCell className="whitespace-nowrap tabular-nums">
                                        {formatDate(user.last_login)}
                                    </TableCell>
                                    <TableCell className="w-12 px-2">
                                        <Button
                                            variant="ghost"
                                            size="icon-sm"
                                            onClick={() => handleEditClick(user)}
                                            title={t('edit')}
                                            aria-label={`${t('edit')}: ${user.username}`}
                                        >
                                            <Pencil className="size-4" />
                                        </Button>
                                    </TableCell>
                                    <TableCell className="w-12 px-2">
                                        <Button
                                            variant="ghost"
                                            size="icon-sm"
                                            className="text-destructive hover:text-destructive"
                                            onClick={() => handleDeleteClick(user)}
                                            title={t('deleteUser')}
                                            aria-label={`${t('deleteUser')}: ${user.username}`}
                                        >
                                            <Trash2 className="size-4" />
                                        </Button>
                                    </TableCell>
                                </TableRow>
                            ))}
                            {users.length === 0 && (
                                <TableRow>
                                    <TableCell
                                        colSpan={9}
                                        className="py-6 text-center text-muted-foreground"
                                    >
                                        {t('noUsersFound')}
                                    </TableCell>
                                </TableRow>
                            )}
                        </TableBody>
                    </Table>
                </div>
            )}

            <Dialog open={dialogOpen} onOpenChange={(next) => !next && handleDialogClose()}>
                <DialogContent
                    closeLabel={t('close')}
                    className="flex max-h-[85vh] flex-col gap-0 p-0 sm:max-w-lg"
                >
                    <DialogHeader className="border-b border-border px-6 py-4 pr-12">
                        <DialogTitle>{t('editUser')}</DialogTitle>
                        <DialogDescription>
                            {selectedUser ? `${t('userId')}: ${selectedUser.id}` : ''}
                        </DialogDescription>
                    </DialogHeader>

                    {selectedUser && (
                        <div className="scrollbar-thin flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-6 py-4">
                            <Field id="edit-user-username" label={t('username')}>
                                <Input
                                    id="edit-user-username"
                                    autoFocus
                                    value={selectedUser.username}
                                    onChange={(event) =>
                                        setSelectedUser({
                                            ...selectedUser,
                                            username: event.target.value,
                                        })
                                    }
                                />
                            </Field>

                            <Field id="edit-user-password" label={t('newPassword')}>
                                <Input
                                    id="edit-user-password"
                                    type="password"
                                    autoComplete="new-password"
                                    value={newPassword}
                                    onChange={(event) => setNewPassword(event.target.value)}
                                />
                            </Field>

                            <Field id="edit-user-email" label={t('email')}>
                                <Input
                                    id="edit-user-email"
                                    type="email"
                                    value={email}
                                    onChange={(event) => setEmail(event.target.value)}
                                />
                            </Field>

                            <Field id="edit-user-first-name" label={t('firstName')}>
                                <Input
                                    id="edit-user-first-name"
                                    value={firstName}
                                    onChange={(event) => setFirstName(event.target.value)}
                                />
                            </Field>

                            <Field id="edit-user-last-name" label={t('lastName')}>
                                <Input
                                    id="edit-user-last-name"
                                    value={lastName}
                                    onChange={(event) => setLastName(event.target.value)}
                                />
                            </Field>

                            <Field id="edit-user-token" label={t('token')}>
                                <Input
                                    id="edit-user-token"
                                    value={token}
                                    onChange={(event) => setToken(event.target.value)}
                                />
                            </Field>

                            <div className="flex items-center gap-2">
                                <Checkbox
                                    id="edit-user-active"
                                    checked={isActive}
                                    onCheckedChange={(checked) => setIsActive(checked === true)}
                                />
                                <label htmlFor="edit-user-active" className="text-sm">
                                    {t('active')}
                                </label>
                            </div>

                            <div className="flex items-center gap-2">
                                <Checkbox
                                    id="edit-user-superuser"
                                    checked={isSuperuser}
                                    onCheckedChange={(checked) => setIsSuperuser(checked === true)}
                                />
                                <label htmlFor="edit-user-superuser" className="text-sm">
                                    {t('superuser')}
                                </label>
                            </div>
                        </div>
                    )}

                    <DialogFooter className="border-t border-border px-6 py-4">
                        <Button variant="ghost" onClick={handleDialogClose}>
                            {t('cancel')}
                        </Button>
                        <Button onClick={handleUserChange}>{t('save')}</Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>

            <div className="mt-auto flex justify-center pt-4">
                <BookPagination
                    totalPages={totalPages}
                    currentPage={parseInt(page ?? '1', 10) || 1}
                    baseUrl={location.pathname}
                />
            </div>
        </div>
    );
};

export default UsersTable;

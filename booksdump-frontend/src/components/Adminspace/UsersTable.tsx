import React, { useEffect, useState } from 'react';
import {useLocation, useParams} from 'react-router-dom';
import {
    Typography,
    Dialog,
    DialogTitle,
    DialogContent,
    Box,
    TableContainer,
    Table,
    TableHead,
    TableRow,
    TableCell,
    TableBody,
    Paper,
    TableSortLabel,
    Stack,
    Checkbox,
    FormControlLabel,
    Button,
    DialogActions,
    Card,
    CardContent,
    CardActions,
    Chip,
    Divider,
    useMediaQuery,
    useTheme,
    Select,
    MenuItem,
    FormControl,
    InputLabel
} from '@mui/material';
import Grid2 from '@mui/material/Grid2';
import IconButton from '@mui/material/IconButton';
import EditIcon from '@mui/icons-material/Edit';
import DeleteForeverIcon from '@mui/icons-material/DeleteForever';
import PersonIcon from '@mui/icons-material/Person';
import EmailIcon from '@mui/icons-material/Email';
import CalendarTodayIcon from '@mui/icons-material/CalendarToday';
import LoginIcon from '@mui/icons-material/Login';
import SupervisorAccountIcon from '@mui/icons-material/SupervisorAccount';
import ArrowUpwardIcon from '@mui/icons-material/ArrowUpward';
import ArrowDownwardIcon from '@mui/icons-material/ArrowDownward';
import { fetchWithAuth } from "../../api/config";
import { formatDate } from "../../utils";
import BookPagination from "../common/BookPagination";
import { useTranslation } from 'react-i18next';
import {buttonLinkSx} from "../commonStyles";
import {StyledTextField} from "../StyledDataItems";
import {styled} from "@mui/material/styles";
import CheckBoxIcon from '@mui/icons-material/CheckBox';
import CheckBoxOutlineBlankIcon from '@mui/icons-material/CheckBoxOutlineBlank';

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

// UserCard component for mobile view
interface UserCardProps {
    user: User;
    onEdit: (user: User) => void;
    onDelete: (user: User) => void;
    t: (key: string) => string;
}

const UserCard: React.FC<UserCardProps> = ({ user, onEdit, onDelete, t }) => {
    return (
        <Card
            sx={{
                mb: 2,
                transition: 'all 0.3s ease',
                '&:hover': {
                    boxShadow: 4,
                    transform: 'translateY(-2px)'
                }
            }}
        >
            <CardContent>
                {/* Header with username and ID */}
                <Box
                    sx={{
                        display: 'flex',
                        justifyContent: 'space-between',
                        alignItems: 'center',
                        mb: 2
                    }}
                >
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                        <PersonIcon color="primary" />
                        <Typography variant="h6" component="div">
                            {user.username}
                        </Typography>
                    </Box>
                    <Chip
                        label={`ID: ${user.id}`}
                        size="small"
                        variant="outlined"
                    />
                </Box>

                <Divider sx={{ mb: 2 }} />

                {/* Email */}
                <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, mb: 1.5 }}>
                    <EmailIcon fontSize="small" sx={{ color: 'text.secondary' }} />
                    <Typography variant="body2" color="text.secondary">
                        {user.email}
                    </Typography>
                </Box>

                {/* Status chips */}
                <Box sx={{ display: 'flex', gap: 1, mb: 2, flexWrap: 'wrap' }}>
                    <Chip
                        label={t('active')}
                        size="small"
                        color={user.active ? 'success' : 'default'}
                        variant={user.active ? 'filled' : 'outlined'}
                        icon={user.active ? <CheckBoxIcon /> : <CheckBoxOutlineBlankIcon />}
                    />
                    {user.is_superuser && (
                        <Chip
                            label={t('superuser')}
                            size="small"
                            color="secondary"
                            icon={<SupervisorAccountIcon />}
                        />
                    )}
                </Box>

                {/* Dates */}
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: 1 }}>
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                        <CalendarTodayIcon fontSize="small" sx={{ color: 'text.secondary' }} />
                        <Typography variant="caption" color="text.secondary">
                            {t('dateJoined')}: {formatDate(user.date_joined)}
                        </Typography>
                    </Box>
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                        <LoginIcon fontSize="small" sx={{ color: 'text.secondary' }} />
                        <Typography variant="caption" color="text.secondary">
                            {t('lastLogin')}: {formatDate(user.last_login)}
                        </Typography>
                    </Box>
                </Box>
            </CardContent>

            <CardActions sx={{ justifyContent: 'flex-end', px: 2, pb: 2, gap: 1 }}>
                {/* Desktop view: Button with text and icon */}
                <Box sx={{ display: { xs: 'none', sm: 'flex' }, gap: 1 }}>
                    <Button
                        size="small"
                        startIcon={<EditIcon />}
                        onClick={() => onEdit(user)}
                        sx={{
                            color: 'primary.main'
                        }}
                    >
                        {t('edit')}
                    </Button>
                    <Button
                        size="small"
                        startIcon={<DeleteForeverIcon />}
                        onClick={() => onDelete(user)}
                        sx={{ color: 'error.main' }}
                    >
                        {t('deleteUser')}
                    </Button>
                </Box>

                {/* Mobile view: IconButton only */}
                <Box sx={{ display: { xs: 'flex', sm: 'none' }, gap: 0.5 }}>
                    <IconButton
                        size="small"
                        onClick={() => onEdit(user)}
                        sx={{
                            color: 'primary.main'
                        }}
                        aria-label={t('edit')}
                    >
                        <EditIcon />
                    </IconButton>
                    <IconButton
                        size="small"
                        onClick={() => onDelete(user)}
                        sx={{ color: 'error.main' }}
                        aria-label={t('deleteUser')}
                    >
                        <DeleteForeverIcon />
                    </IconButton>
                </Box>
            </CardActions>
        </Card>
    );
};

const UsersTable: React.FC = () => {
    const { page } = useParams<{ page: string }>();
    const [users, setUsers] = useState<User[]>([]);
    const [sortOrder, setSortOrder] = useState<boolean>(false);
    const [sortColumn, setSortColumn] = useState<string>('last_login');
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

    // Use theme and media query for responsive design
    const theme = useTheme();
    const isMobile = useMediaQuery(theme.breakpoints.down('md')); // < 960px

    const NarrowTableCell = styled(TableCell)({
        width: '50px',
        padding: '0 8px',
    });

    useEffect(() => {
        const fetchUsers = async () => {
            const limit = 50;
            const offset = (parseInt(page || '1') - 1) * limit;
            try {
                const response = await fetchWithAuth.post('/admin/users', {
                    limit,
                    offset,
                    username: searchQuery,
                    order: sortColumn,
                    desc: sortOrder,
                });
                const data = response.data;
                setUsers(data.users); // Assuming your API returns a field called 'users'
                setTotalPages(data.length);
            } catch (error) {
                console.error(error);
            }
        };

        fetchUsers().then(r => r);
    }, [page, sortOrder, sortColumn, searchQuery]);

    const handleSortRequest = (column: string) => {
        if (sortColumn === column) {
            setSortOrder(prevOrder => !prevOrder); // Toggle sort order
        } else {
            setSortColumn(column);
            setSortOrder(false); // Reset to asc when changing columns
        }
    };

    const handleEditClick = (user: any) => {
        setSelectedUser(user);
        setDialogOpen(true);
        setFirstName(user.first_name); // Set firstName based on the user's first name
        setLastName(user.last_name); // Set lastName based on the user's last name
        setEmail(user.email); // Set email based on the user's email
        setIsActive(user.active); // Set isActive based on the user's active status
        setIsSuperuser(user.is_superuser); // Set isSuperuser based on the user's superuser status
        setToken(user.token); // Set token based on the user's token
    };

    const handleDialogClose = () => {
        setDialogOpen(false);
        setSelectedUser(null);
    };

    const handleDeleteClick = async (user: any) => {
        try {
            const response = await fetchWithAuth.delete(`/admin/user/${user.id}`);
            if (response.status === 200) {
                setUsers(users.filter(u => u.id !== user.id));
            } else {
                console.error('Failed to delete user:', response.statusText);
            }
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
            const response = await fetchWithAuth.post('/admin/user', {
                action: 'update',
                user: updatedUser
            });
            // const data = response.data;
            // setUsers(users.map(user => user.id === data.user.id ? data.user : user));
            if (response.status === 200) {
                // Update the user in the users array
                setUsers(users.map(user => user.id === updatedUser.id ? updatedUser : user));
            }
            handleDialogClose();
        } catch (error) {
            console.error(error);
        }
    };

    return (
        <Box>
            <Typography variant="h6" align="center">{(t('users'))}</Typography>
            <Box display="flex" justifyContent="space-between" alignItems="center" mb={2}>
                <StyledTextField
                    label={t('search')}
                    variant="outlined"
                    value={searchQuery}
                    onChange={(e) => setSearchQuery(e.target.value)}
                />
            </Box>

            {/* Responsive rendering: Cards on mobile, Table on desktop */}
            {isMobile ? (
                // Mobile view: Grid2 with UserCard components
                <Box>
                    {/* Sort controls for mobile */}
                    <Box
                        sx={{
                            display: 'flex',
                            gap: 2,
                            mb: 2,
                            alignItems: 'center',
                            flexWrap: 'wrap'
                        }}
                    >
                        <FormControl size="small" sx={{ minWidth: 200, flex: 1 }}>
                            <InputLabel id="sort-column-label">{t('sortBy')}</InputLabel>
                            <Select
                                labelId="sort-column-label"
                                id="sort-column-select"
                                value={sortColumn}
                                label={t('sortBy')}
                                onChange={(e) => setSortColumn(e.target.value)}
                            >
                                <MenuItem value="date_joined">
                                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                                        <CalendarTodayIcon fontSize="small" />
                                        <span>{t('dateJoined')}</span>
                                    </Box>
                                </MenuItem>
                                <MenuItem value="last_login">
                                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                                        <LoginIcon fontSize="small" />
                                        <span>{t('lastLogin')}</span>
                                    </Box>
                                </MenuItem>
                            </Select>
                        </FormControl>

                        <IconButton
                            onClick={() => setSortOrder(!sortOrder)}
                            sx={{
                                border: 1,
                                borderColor: 'divider',
                                borderRadius: 1,
                                '&:hover': {
                                    backgroundColor: 'action.hover'
                                }
                            }}
                            aria-label={sortOrder ? t('sortDescending') : t('sortAscending')}
                        >
                            {sortOrder ? (
                                <ArrowDownwardIcon />
                            ) : (
                                <ArrowUpwardIcon />
                            )}
                        </IconButton>
                    </Box>

                    {users.length === 0 ? (
                        <Paper sx={{ p: 3, textAlign: 'center' }}>
                            <Typography variant="body1" color="text.secondary">
                                {t('noUsersFound') || 'No users found'}
                            </Typography>
                        </Paper>
                    ) : (
                        <Grid2 container spacing={2}>
                            {users.map((user) => (
                                <Grid2 key={user.id} size={{ xs: 12 }}>
                                    <UserCard
                                        user={user}
                                        onEdit={handleEditClick}
                                        onDelete={handleDeleteClick}
                                        t={t}
                                    />
                                </Grid2>
                            ))}
                        </Grid2>
                    )}
                </Box>
            ) : (
                // Desktop view: Traditional table
                <TableContainer component={Paper}>
                    <Table>
                        <TableHead>
                            <TableRow>
                                <TableCell>{(t('userId'))}</TableCell>
                                <TableCell>{(t('user'))}</TableCell>
                                <TableCell>{(t('email'))}</TableCell>
                                <TableCell>{(t('active'))}</TableCell>
                                <TableCell>{(t('superuser'))}</TableCell>
                                <TableCell sortDirection={sortColumn === 'date_joined' ? (sortOrder ? 'desc' : 'asc') : false}>
                                    <TableSortLabel
                                        active={sortColumn === 'date_joined'}
                                        direction={sortColumn === 'date_joined' ? (sortOrder ? 'desc' : 'asc') : 'asc'}
                                        onClick={() => handleSortRequest('date_joined')}
                                    >
                                        {(t('dateJoined'))}
                                    </TableSortLabel>
                                </TableCell>
                                <TableCell sortDirection={sortColumn === 'last_login' ? (sortOrder ? 'desc' : 'asc') : false}>
                                    <TableSortLabel
                                        active={sortColumn === 'last_login'}
                                        direction={sortColumn === 'last_login' ? (sortOrder ? 'desc' : 'asc') : 'asc'}
                                        onClick={() => handleSortRequest('last_login')}
                                    >
                                        {(t('lastLogin'))}
                                    </TableSortLabel>
                                </TableCell>
                                <TableCell>{(t('edit'))}</TableCell>
                                <TableCell>{(t('deleteUser'))}</TableCell>
                            </TableRow>
                        </TableHead>
                        <TableBody>
                            {users.map((user) => (
                                <TableRow key={user.id}>
                                    <TableCell>{user.id}</TableCell>
                                    <TableCell>{user.username}</TableCell>
                                    <TableCell>{user.email}</TableCell>
                                    <TableCell>
                                        {user.active ? <CheckBoxIcon /> : <CheckBoxOutlineBlankIcon />}
                                    </TableCell>
                                    <TableCell>
                                        {user.is_superuser ? <CheckBoxIcon /> : <CheckBoxOutlineBlankIcon />}
                                    </TableCell>
                                    <TableCell>{formatDate(user.date_joined)}</TableCell>
                                    <TableCell>{formatDate(user.last_login)}</TableCell>
                                    <NarrowTableCell>
                                        <IconButton onClick={() => handleEditClick(user)}>
                                            <EditIcon />
                                        </IconButton>
                                    </NarrowTableCell>
                                    <NarrowTableCell>
                                        <IconButton onClick={() => handleDeleteClick(user)}>
                                            <DeleteForeverIcon />
                                        </IconButton>
                                    </NarrowTableCell>
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                </TableContainer>
            )}

            <Dialog open={dialogOpen} onClose={handleDialogClose} maxWidth="sm" fullWidth>
                <DialogTitle>{(t('editUser'))}</DialogTitle>
                <DialogContent>
                    {selectedUser && (
                        <Box>
                            <React.Fragment>
                                <Box component="span" sx={buttonLinkSx}>ID: {selectedUser.id}</Box>
                            </React.Fragment>
                            <StyledTextField
                                autoFocus
                                margin="dense"
                                label={t('username')}
                                type="text"
                                fullWidth
                                sx={{marginBottom: 2}}
                                value={selectedUser.username}
                                onChange={(e) => setSelectedUser({...selectedUser, username: e.target.value})}
                            />
                            <StyledTextField
                                margin="dense"
                                label={t('newPassword')}
                                type="password"
                                sx={{marginBottom: 2}}
                                fullWidth
                                value={newPassword}
                                onChange={(e) => setNewPassword(e.target.value)}
                            />
                            <StyledTextField
                                margin="dense"
                                label={t('email')}
                                type="email"
                                fullWidth
                                sx={{marginBottom: 2}}
                                value={email}
                                onChange={(e) => setEmail(e.target.value)}
                            />
                            <StyledTextField
                                autoFocus
                                margin="dense"
                                label={t('firstName')}
                                type="text"
                                fullWidth
                                sx={{marginBottom: 2}}
                                value={firstName}
                                onChange={(e) => setFirstName(e.target.value)}
                            />
                            <StyledTextField
                                margin="dense"
                                label={t('lastName')}
                                type="text"
                                fullWidth
                                sx={{marginBottom: 2}}
                                value={lastName}
                                onChange={(e) => setLastName(e.target.value)}
                            />
                            <StyledTextField
                                margin="dense"
                                label={t('token')}
                                type="text"
                                fullWidth
                                value={token}
                                onChange={(e) => setToken(e.target.value)}
                            />
                            <FormControlLabel
                                control={<Checkbox checked={isActive} onChange={(e) => setIsActive(e.target.checked)} />}
                                label={t('active')}
                                sx={{
                                    '& .MuiSvgIcon-root': {
                                        color: (theme) => theme.palette.text.primary,
                                    },
                                    '&.Mui-checked .MuiSvgIcon-root': {
                                        color: (theme) => theme.palette.text.primary,
                                    },
                                }}
                            />
                            <FormControlLabel
                                control={<Checkbox checked={isSuperuser} onChange={(e) => setIsSuperuser(e.target.checked)} />}
                                label={t('superuser')}
                                sx={{
                                    '& .MuiSvgIcon-root': {
                                        color: (theme) => theme.palette.text.primary,
                                    },
                                    '&.Mui-checked .MuiSvgIcon-root': {
                                        color: (theme) => theme.palette.text.primary,
                                    },
                                }}
                            />
                        </Box>
                    )}
                </DialogContent>
                <DialogActions>
                    <Button sx={{ color: 'text.primary' }} onClick={handleDialogClose}>{'Cancel'}</Button>
                    <Button sx={{ color: 'text.primary' }} onClick={handleUserChange}>{'Save'}</Button>
                </DialogActions>
            </Dialog>
            <Stack spacing={3} justifyContent="center" sx={{ marginTop: 2 }}>
                <BookPagination totalPages={totalPages} currentPage={parseInt(page as string)} baseUrl={location.pathname} />
            </Stack>
        </Box>
    );
};

export default UsersTable;

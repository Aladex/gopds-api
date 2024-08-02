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
    Grid,
    Checkbox,
    FormControlLabel, Button, DialogActions
} from '@mui/material';
import IconButton from '@mui/material/IconButton';
import EditIcon from '@mui/icons-material/Edit';
import DeleteForeverIcon from '@mui/icons-material/DeleteForever';
import { fetchWithAuth } from "../../api/config";
import { formatDate } from "../../utils";
import BookPagination from "../common/BookPagination";
import { useTranslation } from 'react-i18next';
import {useCommonStyles} from "../themeStyles";
import {StyledTextField} from "../StyledDataItems";
import {styled} from "@mui/material/styles";
import CheckBoxIcon from '@mui/icons-material/CheckBox';
import CheckBoxOutlineBlankIcon from '@mui/icons-material/CheckBoxOutlineBlank';


const UsersTable: React.FC = () => {
    const { page } = useParams<{ page: string }>();
    const [users, setUsers] = useState<any[]>([]);
    const [sortOrder, setSortOrder] = useState<boolean>(false);
    const [sortColumn, setSortColumn] = useState<string>('last_login');
    const [totalPages, setTotalPages] = useState<number>(0);
    const location = useLocation();
    const { t } = useTranslation();
    const [dialogOpen, setDialogOpen] = useState<boolean>(false);
    const [selectedUser, setSelectedUser] = useState<any>(null);
    const classes = useCommonStyles();
    const [firstName, setFirstName] = useState<string>('');
    const [newPassword, setNewPassword] = useState<string>('');
    const [email, setEmail] = useState<string>('');
    const [lastName, setLastName] = useState<string>('');
    const [token, setToken] = useState<string>('');
    const [isActive, setIsActive] = useState<boolean>(false);
    const [isSuperuser, setIsSuperuser] = useState<boolean>(false);
    const NarrowTableCell = styled(TableCell)({
        width: '50px', // Adjust the width as needed
        padding: '0 8px', // Optional: Adjust padding for better alignment
    });
    const [searchQuery, setSearchQuery] = useState<string>('');

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
        const updatedUser = {
            ...selectedUser,
            first_name: firstName,
            last_name: lastName,
            email: email,
            active: isActive,
            is_superuser: isSuperuser,
            token: token,
        };
        if (newPassword) {
            updatedUser.password = newPassword; // Add password only if it is set
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
            <Dialog open={dialogOpen} onClose={handleDialogClose} maxWidth="sm" fullWidth>
                <DialogTitle>{(t('editUser'))}</DialogTitle>
                <DialogContent>
                    {selectedUser && (
                        <Box>
                            <React.Fragment><button className={classes.buttonLink}>ID: {selectedUser.id}</button></React.Fragment>
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
                                        color: 'black',
                                    },
                                    '&.Mui-checked .MuiSvgIcon-root': {
                                        color: 'black',
                                    },
                                }}
                            />
                            <FormControlLabel
                                control={<Checkbox checked={isSuperuser} onChange={(e) => setIsSuperuser(e.target.checked)} />}
                                label={t('superuser')}
                                sx={{
                                    '& .MuiSvgIcon-root': {
                                        color: 'black',
                                    },
                                    '&.Mui-checked .MuiSvgIcon-root': {
                                        color: 'black',
                                    },
                                }}
                            />
                        </Box>
                    )}
                </DialogContent>
                <DialogActions>
                    <Button color={"secondary"} onClick={handleDialogClose}>{'Cancel'}</Button>
                    <Button color={"secondary"} onClick={handleUserChange}>{'Save'}</Button>
                </DialogActions>
            </Dialog>
            <Grid container spacing={3} justifyContent="center" sx={{ marginTop: 2 }}>
                <BookPagination totalPages={totalPages} currentPage={parseInt(page as string)} baseUrl={location.pathname} />
            </Grid>
        </Box>
    );
};

export default UsersTable;

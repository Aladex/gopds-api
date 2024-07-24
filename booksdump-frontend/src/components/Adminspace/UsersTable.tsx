import React, { useEffect, useState } from 'react';
import { useParams } from 'react-router-dom';
import { Typography, Box, TableContainer, Table, TableHead, TableRow, TableCell, TableBody, Paper } from '@mui/material';
import { fetchWithAuth } from "../../api/config";

const UsersTable: React.FC = () => {
    const { page } = useParams<{ page: string }>();
    const [users, setUsers] = useState<any[]>([]);

    useEffect(() => {
        const fetchUsers = async () => {
            const limit = 50;
            const offset = (parseInt(page || '1') - 1) * limit;
            try {
                const response = await fetchWithAuth.post('/admin/users', {
                    limit,
                    offset,
                    username: '',
                });
                const data = response.data;
                setUsers(data.users); // Assuming your API returns a field called 'users'
                console.log(data);
            } catch (error) {
                console.error(error);
            }
        };

        fetchUsers().then(r => r);
        console.log('UsersTable page:', page);
    }, [page]);

    return (
        <Box>
            <Typography variant="h6" align="center">Users Table - Page {page}</Typography>
            <TableContainer component={Paper}>
                <Table>
                    <TableHead>
                        <TableRow>
                            <TableCell>ID</TableCell>
                            <TableCell>Username</TableCell>
                            <TableCell>Email</TableCell>
                            <TableCell>Active</TableCell>
                            <TableCell>Superuser</TableCell>
                            <TableCell>Date joined</TableCell>
                            <TableCell>Last login</TableCell>
                        </TableRow>
                    </TableHead>
                    <TableBody>
                        {users.map((user) => (
                            <TableRow key={user.id}>
                                <TableCell>{user.id}</TableCell>
                                <TableCell>{user.username}</TableCell>
                                <TableCell>{user.email}</TableCell>
                                <TableCell>{user.active.toString()}</TableCell>
                                <TableCell>{user.is_superuser.toString()}</TableCell>
                                <TableCell>{user.date_joined}</TableCell>
                                <TableCell>{user.last_login}</TableCell>
                            </TableRow>
                        ))}
                    </TableBody>
                </Table>
            </TableContainer>
        </Box>
    );
};

export default UsersTable;

import React, { useEffect, useState } from 'react';
import { Typography, Box, Paper } from '@mui/material';
import { Table, TableContainer, TableHead, TableRow, TableCell, TableBody } from '@mui/material';
import { fetchWithAuth } from "../../api/config";
import { DateTimePicker, LocalizationProvider } from '@mui/x-date-pickers';
import { AdapterDateFns } from '@mui/x-date-pickers/AdapterDateFnsV3';

interface Invite {
    id: string;
    invite: string;
    before_date: string;
}

const InvitesTable: React.FC = () => {
    const [invites, setInvites] = useState<Invite[]>([]);

    useEffect(() => {
        // Fetch all invites from the database
        const fetchInvites = async () => {
            try {
                const response = await fetchWithAuth.get('/admin/invites');
                const data = await response.data;
                setInvites(data.result);
            } catch (error) {
                console.error(error);
            }
        };

        fetchInvites();
    }, []);

    const handleInviteChange = async (invite: Invite) => {
        try {
            const response = await fetchWithAuth.post('/admin/invite', {
                action: 'update',
                invite: invite,
            });
            if (response.status === 200) {
                setInvites(invites.map(inv => inv.id === invite.id ? invite : inv));
            }
        } catch (error) {
            console.error(error);
        }
    };

    const handleDateChange = (inviteId: string) => (date: Date | null) => {
        if (!date) return;
        const invite = invites.find(inv => inv.id === inviteId);
        if (!invite) return;
        const updatedInvite = { ...invite, before_date: date.toISOString() };
        handleInviteChange(updatedInvite).then(r => r);
    };
    return (
        <LocalizationProvider dateAdapter={AdapterDateFns}>
            <Box>
                <Typography variant="h6" align="center">Invites Table</Typography>
                <TableContainer component={Paper}>
                    <Table>
                        <TableHead>
                            <TableRow>
                                <TableCell>Invite ID</TableCell>
                                <TableCell>Invite Code</TableCell>
                                <TableCell>Invite Before</TableCell>
                            </TableRow>
                        </TableHead>
                        <TableBody>
                            {invites.map((invite) => (
                                <TableRow key={invite.id}>
                                    <TableCell>{invite.id}</TableCell>
                                    <TableCell>{invite.invite}</TableCell>
                                    <TableCell>
                                        <DateTimePicker
                                            value={invite.before_date ? new Date(invite.before_date) : null}
                                            onChange={(newValue) => handleDateChange(invite.id)(newValue)}
                                        />
                                    </TableCell>
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                </TableContainer>
            </Box>
        </LocalizationProvider>
    );
};

export default InvitesTable;

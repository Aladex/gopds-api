import React, { useEffect, useState } from 'react';
import { Typography, Box, Paper, IconButton, Dialog, DialogTitle, DialogContent, DialogActions, Button } from '@mui/material';
import { Table, TableContainer, TableHead, TableRow, TableCell, TableBody } from '@mui/material';
import { fetchWithAuth } from "../../api/config";
import { DateTimePicker, LocalizationProvider } from '@mui/x-date-pickers';
import { AdapterDateFns } from '@mui/x-date-pickers/AdapterDateFnsV3';
import DeleteForeverIcon from "@mui/icons-material/DeleteForever";
import AddIcon from "@mui/icons-material/Add";
import {useTranslation} from "react-i18next";
import { styled } from '@mui/material/styles';
import {StyledTextField} from "../StyledDataItems";

interface Invite {
    id?: string;
    invite: string;
    before_date: string;
}

const InvitesTable: React.FC = () => {
    const [invites, setInvites] = useState<Invite[]>([]);
    const [dialogOpen, setDialogOpen] = useState<boolean>(false);
    const [newInviteCode, setNewInviteCode] = useState<string>('');
    const { t } = useTranslation();
    const NarrowTableCell = styled(TableCell)({
        width: '50px', // Adjust the width as needed
        padding: '0 8px', // Optional: Adjust padding for better alignment
    });

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

    const handleDeleteInvite = async (invite: Invite) => {
        try {
            const response = await fetchWithAuth.post('/admin/invite', {
                action: 'delete',
                invite: invite,
            });
            if (response.status === 200) {
                setInvites(invites.filter(inv => inv.id !== invite.id));
            }
        } catch (error) {
            console.error(error);
        }
    };

    const handleDateChange = (inviteId?: string) => (date: Date | null) => {
        if (!date || !inviteId) return;
        const invite = invites.find(inv => inv.id === inviteId);
        if (!invite) return;
        const updatedInvite = { ...invite, before_date: date.toISOString() };
        handleInviteChange(updatedInvite).then(r => r);
    };

    const handleAddInvite = async () => {
        const newInvite = {
            invite: newInviteCode,
            before_date: new Date(new Date().setMonth(new Date().getMonth() + 1)).toISOString(),
        };
        try {
            const response = await fetchWithAuth.post('/admin/invite', {
                action: 'create',
                invite: newInvite,
            });
            if (response.status === 200) {
                // Fetch the updated list of invites
                const fetchResponse = await fetchWithAuth.get('/admin/invites');
                const data = await fetchResponse.data;
                setInvites(data.result);
                setDialogOpen(false);
                setNewInviteCode('');
            }
        } catch (error) {
            console.error(error);
        }
    };

    return (
        <LocalizationProvider dateAdapter={AdapterDateFns}>
            <Box>
                <Box display="flex" justifyContent="space-between" alignItems="center">
                    <Typography variant="h6" align="center">{t('invites')}</Typography>
                    <IconButton onClick={() => setDialogOpen(true)}>
                        <AddIcon />
                    </IconButton>
                </Box>
                <TableContainer component={Paper}>
                    <Table>
                        <TableHead>
                            <TableRow>
                                <TableCell>{t('inviteId')}</TableCell>
                                <TableCell>{t('inviteCode')}</TableCell>
                                <TableCell>{t('beforeDate')}</TableCell>
                                <TableCell>{t('actions')}</TableCell>
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
                                            ampm={false}
                                        />
                                    </TableCell>
                                    <NarrowTableCell>
                                        <IconButton onClick={() => handleDeleteInvite(invite)}>
                                            <DeleteForeverIcon />
                                        </IconButton>
                                    </NarrowTableCell>
                                </TableRow>
                            ))}
                        </TableBody>
                    </Table>
                </TableContainer>
                <Dialog open={dialogOpen} onClose={() => setDialogOpen(false)}>
                    <DialogTitle>{t('addInvite')}</DialogTitle>
                    <DialogContent>
                        <StyledTextField
                            autoFocus
                            margin="dense"
                            label="Invite Code"
                            type="text"
                            fullWidth
                            value={newInviteCode}
                            onChange={(e) => setNewInviteCode(e.target.value)}
                        />
                    </DialogContent>
                    <DialogActions>
                        <Button sx={{ color: 'text.primary' }} onClick={() => setDialogOpen(false)}>
                            Cancel
                        </Button>
                        <Button sx={{ color: 'text.primary' }} onClick={handleAddInvite}>
                            Add
                        </Button>
                    </DialogActions>
                </Dialog>
            </Box>
        </LocalizationProvider>
    );
};

export default InvitesTable;

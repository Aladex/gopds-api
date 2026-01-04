import React, { useState, useEffect, useCallback, useMemo } from 'react';
import QRCode from 'qrcode';
import { useTheme } from '@mui/material/styles';
import {
    Dialog,
    DialogTitle,
    DialogContent,
    IconButton,
    Box,
    Tabs,
    Tab,
    Typography,
    Button,
} from '@mui/material';
import { Close as CloseIcon } from '@mui/icons-material';

interface DonateModalProps {
    open: boolean;
    onClose: () => void;
}

const DonateModal: React.FC<DonateModalProps> = ({ open, onClose }) => {
    const theme = useTheme();
    const [activeTab, setActiveTab] = useState(0);
    const [qrCodes, setQrCodes] = useState<{[key: string]: string}>({});

    // Crypto addresses - use useMemo so the object is not recreated
    const cryptoAddresses = useMemo(() => ({
        bitcoin: 'bc1qv2pjsnkprer35u2whuquztvnvnggjsrqu4q43f',
        ethereum: '0xD053A0fE7C450b57da9FF169620EB178644b54C9',
        usdt: 'TTE5dv9w9RSDMJ6k3tnpfuehH8UX9Fy4Ec'
    }), []);

    const qrColors = useMemo(() => ({
        dark: theme.palette.text.primary,
        light: theme.palette.background.paper,
    }), [theme.palette.background.paper, theme.palette.text.primary]);

    const generateQRCodes = useCallback(async () => {
        const codes: {[key: string]: string} = {};
        try {
            for (const [currency, address] of Object.entries(cryptoAddresses)) {
                codes[currency] = await QRCode.toDataURL(address, {
                    width: 200,
                    margin: 2,
                    color: qrColors,
                });
            }
            setQrCodes(codes);
        } catch (error) {
            console.error('Error generating QR codes:', error);
        }
    }, [cryptoAddresses, qrColors]);

    useEffect(() => {
        if (open) {
            // Generate QR codes for crypto addresses
            generateQRCodes().catch((error) => {
                console.error('Error generating QR codes:', error);
            });
        }
    }, [open, generateQRCodes]);

    const handleTabChange = (event: React.SyntheticEvent, newValue: number) => {
        setActiveTab(newValue);
    };

    const copyToClipboard = (text: string) => {
        navigator.clipboard.writeText(text);
        // Can add copy notification here
    };

    return (
        <Dialog
            open={open}
            onClose={onClose}
            maxWidth="sm"
            fullWidth
            PaperProps={{
                sx: {
                    backgroundColor: theme.palette.mode === 'dark' ? '#1e1e1e' : '#ffffff',
                    color: theme.palette.text.primary,
                    maxHeight: '90vh',
                },
            }}
        >
            <DialogTitle sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', pb: 1 }}>
                <Typography variant="h6">Поддержать проект</Typography>
                <IconButton onClick={onClose} size="small">
                    <CloseIcon />
                </IconButton>
            </DialogTitle>

            <Box sx={{ borderBottom: 1, borderColor: 'divider' }}>
                <Tabs
                    value={activeTab}
                    onChange={handleTabChange}
                    variant="scrollable"
                    scrollButtons="auto"
                    allowScrollButtonsMobile
                    sx={{
                        '& .MuiTab-root': {
                            minWidth: { xs: 80, sm: 100 },
                            fontSize: { xs: '0.75rem', sm: '0.875rem' },
                            px: { xs: 1, sm: 2 },
                        },
                    }}
                >
                    <Tab label="Tinkoff" />
                    <Tab label="Bitcoin" />
                    <Tab label="Ethereum" />
                    <Tab label="USDT" />
                    <Tab label="PayPal" />
                    <Tab label="BuyMeACoffee" />
                </Tabs>
            </Box>

            <DialogContent sx={{ pt: 3 }}>
                {activeTab === 0 && (
                    <Box>
                        <Typography variant="h6" gutterBottom>Отправить деньги на Tinkoff</Typography>
                        <Box sx={{ mb: 2 }}>
                            <Typography variant="body2" color="text.secondary" gutterBottom>Номер карты:</Typography>
                            <Box sx={{ display: 'flex', gap: 1, alignItems: 'center', flexWrap: 'wrap' }}>
                                <Typography variant="body1" sx={{ fontFamily: 'monospace', flexGrow: 1 }}>
                                    5536 9139 9418 6852
                                </Typography>
                                <Button
                                    variant="contained"
                                    size="small"
                                    onClick={() => copyToClipboard('5536913994186852')}
                                >
                                    Копировать
                                </Button>
                            </Box>
                        </Box>
                        <Typography variant="body2" color="text.secondary" sx={{ mb: 1, fontStyle: 'italic' }}>
                            Или по ссылке:
                        </Typography>
                        <Button
                            variant="contained"
                            color="secondary"
                            href="https://tbank.ru/cf/634wAzuZc0Z"
                            target="_blank"
                            rel="noopener noreferrer"
                            fullWidth
                            size="large"
                        >
                            Т-БАНК
                        </Button>
                    </Box>
                )}

                {activeTab === 1 && (
                    <Box>
                        <Typography variant="h6" gutterBottom>Отправить донат Bitcoin</Typography>
                        <Box sx={{ p: 2, border: 1, borderColor: 'divider', borderRadius: 1, bgcolor: 'background.default' }}>
                            <Typography variant="subtitle2" gutterBottom><strong>Bitcoin:</strong></Typography>
                            <Box sx={{ display: 'flex', gap: 1, alignItems: 'center', flexWrap: 'wrap', mb: 2 }}>
                                <Typography variant="body2" sx={{ fontFamily: 'monospace', wordBreak: 'break-all', flexGrow: 1 }}>
                                    bc1qv2pjsnkprer35u2whuquztvnvnggjsrqu4q43f
                                </Typography>
                                <Button
                                    variant="contained"
                                    size="small"
                                    onClick={() => copyToClipboard('bc1qv2pjsnkprer35u2whuquztvnvnggjsrqu4q43f')}
                                >
                                    Копировать
                                </Button>
                            </Box>
                            {qrCodes.bitcoin && (
                                <Box sx={{ display: 'flex', justifyContent: 'center', mt: 2 }}>
                                    <img src={qrCodes.bitcoin} alt="QR Code для Bitcoin" style={{ maxWidth: 200, borderRadius: 8 }} />
                                </Box>
                            )}
                        </Box>
                    </Box>
                )}

                {activeTab === 2 && (
                    <Box>
                        <Typography variant="h6" gutterBottom>Отправить донат Ethereum (ERC20)</Typography>
                        <Box sx={{ p: 2, border: 1, borderColor: 'divider', borderRadius: 1, bgcolor: 'background.default' }}>
                            <Typography variant="subtitle2" gutterBottom><strong>Ethereum:</strong></Typography>
                            <Box sx={{ display: 'flex', gap: 1, alignItems: 'center', flexWrap: 'wrap', mb: 2 }}>
                                <Typography variant="body2" sx={{ fontFamily: 'monospace', wordBreak: 'break-all', flexGrow: 1 }}>
                                    0xD053A0fE7C450b57da9FF169620EB178644b54C9
                                </Typography>
                                <Button
                                    variant="contained"
                                    size="small"
                                    onClick={() => copyToClipboard('0xD053A0fE7C450b57da9FF169620EB178644b54C9')}
                                >
                                    Копировать
                                </Button>
                            </Box>
                            {qrCodes.ethereum && (
                                <Box sx={{ display: 'flex', justifyContent: 'center', mt: 2 }}>
                                    <img src={qrCodes.ethereum} alt="QR Code для Ethereum" style={{ maxWidth: 200, borderRadius: 8 }} />
                                </Box>
                            )}
                        </Box>
                    </Box>
                )}

                {activeTab === 3 && (
                    <Box>
                        <Typography variant="h6" gutterBottom>Отправить донат USDT</Typography>
                        <Box sx={{ p: 2, border: 1, borderColor: 'divider', borderRadius: 1, bgcolor: 'background.default' }}>
                            <Typography variant="subtitle2" gutterBottom><strong>USDT (TRON):</strong></Typography>
                            <Box sx={{ display: 'flex', gap: 1, alignItems: 'center', flexWrap: 'wrap', mb: 2 }}>
                                <Typography variant="body2" sx={{ fontFamily: 'monospace', wordBreak: 'break-all', flexGrow: 1 }}>
                                    TTE5dv9w9RSDMJ6k3tnpfuehH8UX9Fy4Ec
                                </Typography>
                                <Button
                                    variant="contained"
                                    size="small"
                                    onClick={() => copyToClipboard('TTE5dv9w9RSDMJ6k3tnpfuehH8UX9Fy4Ec')}
                                >
                                    Копировать
                                </Button>
                            </Box>
                            {qrCodes.usdt && (
                                <Box sx={{ display: 'flex', justifyContent: 'center', mt: 2 }}>
                                    <img src={qrCodes.usdt} alt="QR Code для USDT" style={{ maxWidth: 200, borderRadius: 8 }} />
                                </Box>
                            )}
                        </Box>
                    </Box>
                )}

                {activeTab === 4 && (
                    <Box>
                        <Typography variant="h6" gutterBottom>Отправить донат через PayPal</Typography>
                        <Button
                            variant="contained"
                            color="secondary"
                            href="https://www.paypal.com/donate/?hosted_button_id=PJ9RC6X742T62"
                            target="_blank"
                            rel="noopener noreferrer"
                            fullWidth
                            size="large"
                        >
                            Открыть PayPal
                        </Button>
                    </Box>
                )}

                {activeTab === 5 && (
                    <Box>
                        <Typography variant="h6" gutterBottom>Отправить донат через BuyMeACoffee</Typography>
                        <Button
                            variant="contained"
                            color="secondary"
                            href="https://www.buymeacoffee.com/aladex"
                            target="_blank"
                            rel="noopener noreferrer"
                            fullWidth
                            size="large"
                        >
                            Открыть BuyMeACoffee
                        </Button>
                    </Box>
                )}
            </DialogContent>
        </Dialog>
    );
};

export default DonateModal;

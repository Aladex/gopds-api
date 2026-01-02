import React from 'react';
import { Box, Skeleton } from '@mui/material';
import { keyframes } from '@emotion/react';

const fadeIn = keyframes`
  from { opacity: 0; transform: translateY(4px); }
  to { opacity: 1; transform: translateY(0); }
`;

const AppSkeleton: React.FC = () => {
    return (
        <Box
            sx={{
                minHeight: '100vh',
                bgcolor: 'background.default',
                animation: `${fadeIn} 300ms ease-out`,
            }}
        >
            <Box sx={{ bgcolor: '#2f2f2f', px: 2, py: 1.5 }}>
                <Box
                    sx={{
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'space-between',
                        gap: 2,
                        flexWrap: 'wrap',
                    }}
                >
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 2 }}>
                        <Skeleton variant="circular" width={28} height={28} sx={{ bgcolor: 'grey.700' }} />
                        <Skeleton variant="rectangular" width={160} height={24} sx={{ bgcolor: 'grey.700' }} />
                        <Box sx={{ display: { xs: 'none', sm: 'flex' }, gap: 1 }}>
                            <Skeleton variant="rectangular" width={90} height={22} sx={{ bgcolor: 'grey.700' }} />
                            <Skeleton variant="rectangular" width={90} height={22} sx={{ bgcolor: 'grey.700' }} />
                            <Skeleton variant="rectangular" width={90} height={22} sx={{ bgcolor: 'grey.700' }} />
                        </Box>
                    </Box>
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
                        <Skeleton variant="circular" width={28} height={28} sx={{ bgcolor: 'grey.700' }} />
                        <Skeleton variant="circular" width={28} height={28} sx={{ bgcolor: 'grey.700' }} />
                        <Skeleton variant="rectangular" width={72} height={24} sx={{ bgcolor: 'grey.700', borderRadius: 12 }} />
                    </Box>
                </Box>
            </Box>

            <Box sx={{ px: 2, py: 2 }}>
                <Skeleton variant="rectangular" height={42} sx={{ mb: 2, borderRadius: 2 }} />

                <Box
                    sx={{
                        display: { xs: 'block', md: 'grid' },
                        gridTemplateColumns: { md: '2fr 1fr' },
                        gap: 2,
                    }}
                >
                    <Box>
                        {[0, 1, 2, 3].map((item) => (
                            <Box
                                key={item}
                                sx={{
                                    display: 'flex',
                                    gap: 2,
                                    p: 2,
                                    mb: 2,
                                    borderRadius: 2,
                                    bgcolor: 'background.paper',
                                    boxShadow: 1,
                                }}
                            >
                                <Skeleton variant="rectangular" width={72} height={96} sx={{ borderRadius: 1 }} />
                                <Box sx={{ flex: 1 }}>
                                    <Skeleton variant="text" width="60%" height={24} />
                                    <Skeleton variant="text" width="40%" height={20} />
                                    <Skeleton variant="text" width="90%" height={18} />
                                    <Skeleton variant="text" width="75%" height={18} />
                                </Box>
                            </Box>
                        ))}
                    </Box>
                    <Box sx={{ display: { xs: 'none', md: 'block' } }}>
                        {[0, 1, 2].map((item) => (
                            <Box
                                key={item}
                                sx={{
                                    p: 2,
                                    mb: 2,
                                    borderRadius: 2,
                                    bgcolor: 'background.paper',
                                    boxShadow: 1,
                                }}
                            >
                                <Skeleton variant="text" width="70%" height={22} />
                                <Skeleton variant="text" width="50%" height={18} />
                                <Skeleton variant="text" width="80%" height={18} />
                            </Box>
                        ))}
                    </Box>
                </Box>
            </Box>
        </Box>
    );
};

export default AppSkeleton;

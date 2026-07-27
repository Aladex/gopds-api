import React from 'react';
import { Box, Card, Skeleton, Stack } from '@mui/material';
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
            {/* Header */}
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

            {/* Book cards list */}
            <Stack spacing={0} sx={{ px: 2, py: 1 }}>
                {[0, 1, 2, 3].map((item) => (
                    <Box maxWidth={1200} mx="auto" key={item} sx={{ width: '100%' }}>
                        <Card sx={{ boxShadow: 2, p: 2, my: 2 }}>
                            <Stack direction={{ xs: 'column', md: 'row' }} spacing={2}>
                                {/* Left: cover + details */}
                                <Box sx={{ flex: { xs: 1, md: 3 }, minWidth: 0 }}>
                                    <Stack direction={{ xs: 'column', md: 'row' }} spacing={2}>
                                        {/* Cover */}
                                        <Box sx={{ flex: { xs: 1, md: 1 }, maxWidth: { md: '33.33%' } }}>
                                            <Skeleton
                                                variant="rectangular"
                                                sx={{
                                                    width: '100%',
                                                    aspectRatio: '2/3',
                                                    borderRadius: 1,
                                                }}
                                            />
                                        </Box>
                                        {/* Text details */}
                                        <Box sx={{ flex: { xs: 1, md: 2 }, py: 1 }}>
                                            <Skeleton variant="text" width="70%" height={32} />
                                            <Skeleton variant="text" width="45%" height={20} sx={{ mt: 1 }} />
                                            <Skeleton variant="text" width="40%" height={20} />
                                            <Skeleton variant="text" width="30%" height={20} sx={{ mt: 1 }} />
                                            <Box sx={{ display: 'flex', gap: 1, mt: 2 }}>
                                                <Skeleton variant="rectangular" width={80} height={24} sx={{ borderRadius: 8 }} />
                                                <Skeleton variant="rectangular" width={100} height={24} sx={{ borderRadius: 8 }} />
                                            </Box>
                                            <Box sx={{ display: 'flex', gap: 1, mt: 1 }}>
                                                <Skeleton variant="rectangular" width={70} height={24} sx={{ borderRadius: 8 }} />
                                            </Box>
                                        </Box>
                                    </Stack>
                                    {/* Annotation */}
                                    <Box sx={{ mt: 1, px: 2 }}>
                                        <Skeleton variant="text" width="95%" height={18} />
                                        <Skeleton variant="text" width="85%" height={18} />
                                        <Skeleton variant="text" width="60%" height={18} />
                                    </Box>
                                </Box>
                                {/* Right: download buttons */}
                                <Box sx={{ flex: { xs: 1, md: 1 }, display: 'flex', flexDirection: 'column', gap: 1, alignItems: { xs: 'stretch', md: 'flex-start' }, pt: 1 }}>
                                    <Skeleton variant="rectangular" width={120} height={36} sx={{ borderRadius: 1 }} />
                                    <Skeleton variant="rectangular" width={120} height={36} sx={{ borderRadius: 1 }} />
                                    <Skeleton variant="rectangular" width={120} height={36} sx={{ borderRadius: 1 }} />
                                    <Skeleton variant="rectangular" width={120} height={36} sx={{ borderRadius: 1 }} />
                                </Box>
                            </Stack>
                        </Card>
                    </Box>
                ))}
            </Stack>
        </Box>
    );
};

export default AppSkeleton;
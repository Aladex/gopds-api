// src/components/common/CoverLoader.tsx
import React, { useState } from 'react';
import { Box, CircularProgress } from '@mui/material';

interface CoverLoaderProps {
    imageUrl: string;
    alt: string;
}

const CoverLoader: React.FC<CoverLoaderProps> = ({ imageUrl, alt }) => {
    const [loading, setLoading] = useState(true);

    return (
        <Box
            position="relative"
            width="100%"
            height="300px"
            sx={{ objectFit: 'scale-down' }}
        >
            {loading && (
                <Box
                    position="absolute"
                    top="0"
                    left="0"
                    width="100%"
                    height="100%"
                    display="flex"
                    justifyContent="center"
                    alignItems="center"
                    bgcolor="rgba(255, 255, 255, 0.5)"
                    style={{ backdropFilter: 'blur(10px)', zIndex: 1 }} // Ensure loader is on top
                >
                    <img
                        src="/cover-loading.png"
                        alt="Loading cover"
                        style={{
                            width: '100%',
                            height: '100%',
                            objectFit: 'scale-down',
                            filter: 'blur(5px)',
                            position: 'absolute',
                            top: 0,
                            left: 0,
                            zIndex: 0
                        }}
                    />
                    <CircularProgress style={{ zIndex: 1 }} />
                </Box>
            )}
            <img
                src={imageUrl}
                alt={alt}
                onLoad={() => setLoading(false)}
                style={{
                    display: loading ? 'none' : 'block',
                    width: '100%',
                    height: '100%',
                    objectFit: 'scale-down'
                }}
            />
        </Box>
    );
};

export default CoverLoader;

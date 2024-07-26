import React, { useState } from 'react';
import { Box, CircularProgress } from '@mui/material';
import '../styles/CoverLoader.css';

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
                    bgcolor="rgba(255, 255, 255, 0.7)"
                    zIndex={2} // Ensure loader is on top
                >
                    <CircularProgress />
                </Box>
            )}
            <img
                src="/cover-loading.png"
                alt="Loading cover"
                className="image-with-border loading-image"
                style={{
                    display: loading ? 'block' : 'none',
                }}
            />
            <img
                src={imageUrl}
                alt={alt}
                onLoad={() => setLoading(false)}
                className="image-with-border"
                style={{
                    display: loading ? 'none' : 'block',
                }}
            />
        </Box>
    );
};

export default CoverLoader;

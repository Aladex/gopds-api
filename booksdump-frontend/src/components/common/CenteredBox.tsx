import React from 'react';
import { Box, Card, useMediaQuery } from '@mui/material';
import theme from '../../theme';
import { Link } from 'react-router-dom';

interface ILoginCenteredBoxProps {
    children: React.ReactNode;
}

const LoginCenteredBox: React.FC<ILoginCenteredBoxProps> = ({ children }) => {
    const isMobile = useMediaQuery(theme.breakpoints.down('sm'));

    return (
        <>
            {/* Main container with form */}
            <Box
                sx={{
                    display: 'flex',
                    justifyContent: 'center',
                    alignItems: 'center',
                    height: '100vh',
                    position: 'relative',
                    zIndex: 1
                }}
            >
                <Card sx={{
                    padding: 3,
                    position: 'relative',
                    zIndex: 2,
                    [theme.breakpoints.up('md')]: { minWidth: 700 }
                }}>
                    {children}
                </Card>

                {/* Fixed background Danny DeVito image - positioned to not overlap form */}
                <Link
                    to="/registration"
                    style={{
                        position: 'fixed',
                        bottom: 0,
                        right: 0,
                        zIndex: 1,
                        opacity: 0.7,
                        transition: 'opacity 0.3s ease',
                        // Ensure it doesn't interfere with form interaction
                        pointerEvents: 'auto'
                    }}
                    onMouseEnter={(e) => e.currentTarget.style.opacity = '1'}
                    onMouseLeave={(e) => e.currentTarget.style.opacity = '0.7'}
                >
                    <img
                        src="/devito_back.png"
                        alt="Registration"
                        style={{
                            width: isMobile ? '60px' : '200px',
                            height: isMobile ? '40px' : '133px',
                            display: 'block',
                            border: 'none',
                            userSelect: 'none',
                            pointerEvents: 'auto',
                            // Add some margin to prevent overlap with form
                            marginBottom: isMobile ? '10px' : '20px',
                            marginRight: isMobile ? '10px' : '20px'
                        }}
                    />
                </Link>
            </Box>
        </>
    );
};

export default LoginCenteredBox;
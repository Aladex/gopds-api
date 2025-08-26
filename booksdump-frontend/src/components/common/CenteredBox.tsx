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

                {/* Fixed background Danny DeVito image - seamlessly blended into corner */}
                <Link
                    to="/registration"
                    style={{
                        position: 'fixed',
                        bottom: 0,
                        right: 0,
                        zIndex: 1,
                        opacity: 0.6,
                        transition: 'opacity 0.3s ease',
                        pointerEvents: 'auto'
                    }}
                    onMouseEnter={(e) => e.currentTarget.style.opacity = '0.9'}
                    onMouseLeave={(e) => e.currentTarget.style.opacity = '0.6'}
                >
                    <img
                        src="/devito_back.png"
                        alt="Registration"
                        style={{
                            width: isMobile ? '60px' : '180px',
                            height: isMobile ? '40px' : '120px',
                            display: 'block',
                            border: 'none',
                            userSelect: 'none',
                            pointerEvents: 'auto'
                        }}
                    />
                </Link>
            </Box>
        </>
    );
};

export default LoginCenteredBox;
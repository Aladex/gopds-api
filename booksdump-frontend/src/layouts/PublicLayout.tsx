// src/layouts/PublicLayout.tsx
import React from 'react';
import { Outlet } from 'react-router-dom';
import { Container } from '@mui/material';

const PublicLayout: React.FC = () => (
    <Container>
        <Outlet />
    </Container>
);

export default PublicLayout;

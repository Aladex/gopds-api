// src/routes/notFoundRoutes.tsx
import React from 'react';
import { Route } from 'react-router-dom';
import NotFoundRoute from '../components/NotFoundRoute';

const notFoundRoutes = (
    <Route path="*" element={<NotFoundRoute />} />
);

export default notFoundRoutes;

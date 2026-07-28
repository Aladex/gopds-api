// src/routes/notFoundRoutes.tsx
import React from 'react';
import { Route } from 'react-router';
import NotFoundRoute from '@/app/NotFoundRoute';

const notFoundRoutes = <Route path="*" element={<NotFoundRoute />} />;

export default notFoundRoutes;

// src/routes/adminRoutes.tsx
import React from 'react';
import { Route, Navigate } from 'react-router';
import AdminSpace from '@/features/admin/AdminPanel';
import PrivateRoute from '@/app/PrivateRoute';
import TitleSetter from '@/app/TitleSetter';

const adminRoutes = (
    <>
        <Route
            path="/admin"
            element={
                <PrivateRoute requireSuperuser={true}>
                    <Navigate to="/admin/users" replace />
                </PrivateRoute>
            }
        />
        <Route
            path="/admin/*"
            element={
                <PrivateRoute requireSuperuser={true}>
                    <TitleSetter titleKey="routeTitles.admin">
                        <AdminSpace />
                    </TitleSetter>
                </PrivateRoute>
            }
        />
    </>
);

export default adminRoutes;

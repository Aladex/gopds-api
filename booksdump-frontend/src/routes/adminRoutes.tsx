import React from 'react';
import { Route, Navigate } from 'react-router-dom';
import AdminSpace from '../components/Adminspace/AdminPanel';
import PrivateRoute from '../components/PrivateRoute';
import TitleSetter from '../components/common/TitleSetter';

const adminRoutes = (
    <>
        <Route path="/admin" element={<PrivateRoute><Navigate to="/admin/users" replace /></PrivateRoute>} />
        <Route
            path="/admin/*"
            element={
                <PrivateRoute>
                    <TitleSetter titleKey="routeTitles.admin">
                        <AdminSpace />
                    </TitleSetter>
                </PrivateRoute>
            }
        />
    </>
);

export default adminRoutes;

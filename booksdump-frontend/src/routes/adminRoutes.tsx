import React from 'react';
import { Route, Navigate } from 'react-router-dom';
import TitleSetter from '../components/common/TitleSetter';
import AdminSpace from '../components/Adminspace/AdminPanel';
import PrivateRoute from '../components/PrivateRoute';

const adminRoutes = (
    <>
        <Route path="/" element={<PrivateRoute><Navigate to="/admin/users" replace /></PrivateRoute>} />
        <Route path="/admin" element={<PrivateRoute><Navigate to="/admin/users" replace /></PrivateRoute>} />
        <Route path="/admin/users" element={<PrivateRoute><TitleSetter titleKey="routeTitles.adminUsers"><AdminSpace /></TitleSetter></PrivateRoute>} />
        <Route path="/admin/invites" element={<PrivateRoute><TitleSetter titleKey="routeTitles.adminInvites"><AdminSpace /></TitleSetter></PrivateRoute>} />
    </>
);

export default adminRoutes;
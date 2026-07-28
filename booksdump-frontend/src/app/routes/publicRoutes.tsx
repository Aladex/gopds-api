// src/routes/publicRoutes.tsx
import React from 'react';
import { Route } from 'react-router';
import Login from '@/features/auth/Login';
import Registration from '@/features/auth/Registration';
import ForgotPassword from '@/features/auth/ForgotPassword';
import TitleSetter from '@/app/TitleSetter';
import PublicRoute from '@/app/PublicRoute';
import ChangePassword from '@/features/auth/ChangePassword';
import Activation from '@/features/auth/Activation';

const publicRoutes = (
    <Route path="/" element={<PublicRoute />}>
        <Route
            path="/login"
            element={
                <TitleSetter titleKey="routeTitles.login">
                    <Login />
                </TitleSetter>
            }
        />
        <Route
            path="/registration"
            element={
                <TitleSetter titleKey="routeTitles.registration">
                    <Registration />
                </TitleSetter>
            }
        />
        <Route
            path="/forgot-password"
            element={
                <TitleSetter titleKey="routeTitles.forgotPassword">
                    <ForgotPassword />
                </TitleSetter>
            }
        />
        <Route
            path="/change-password/:token"
            element={
                <TitleSetter titleKey="routeTitles.forgotPassword">
                    <ChangePassword />
                </TitleSetter>
            }
        />
        <Route
            path="/activate/:token"
            element={
                <TitleSetter titleKey="routeTitles.activation">
                    <Activation />
                </TitleSetter>
            }
        />
    </Route>
);

export default publicRoutes;

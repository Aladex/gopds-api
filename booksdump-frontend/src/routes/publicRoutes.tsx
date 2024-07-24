// src/routes/publicRoutes.tsx
import React from 'react';
import { Route } from 'react-router-dom';
import Login from '../components/Auth/Login';
import Registration from '../components/Auth/Registration';
import ForgotPassword from '../components/Auth/ForgotPassword';
import TitleSetter from '../components/common/TitleSetter';
import ErrorPage from '../components/Service/ErrorPage';
import PublicRoute from "../components/PublicRoute";

const publicRoutes = (
    <>
            <Route path="/" element={<PublicRoute />}>
                    <Route path="/login" element={<TitleSetter titleKey="routeTitles.login"><Login /></TitleSetter>} />
                    <Route path="/registration" element={<TitleSetter titleKey="routeTitles.registration"><Registration /></TitleSetter>} />
                    <Route path="/forgot-password" element={<TitleSetter titleKey="routeTitles.forgotPassword"><ForgotPassword /></TitleSetter>} />
                    <Route path="/404" element={<TitleSetter titleKey="routeTitles.error"><ErrorPage /></TitleSetter>} />
                    <Route path="*" element={<TitleSetter titleKey="routeTitles.error"><ErrorPage /></TitleSetter>} />
            </Route>
    </>
);

export default publicRoutes;

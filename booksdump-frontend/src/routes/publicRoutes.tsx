// src/routes/publicRoutes.tsx
import React from 'react';
import { Route } from 'react-router-dom';
import Login from '../components/Auth/Login';
import Registration from '../components/Auth/Registration';
import ForgotPassword from '../components/Auth/ForgotPassword';
import TitleSetter from '../components/common/TitleSetter';

const publicRoutes = (
    <>
        <Route path="/login" element={<TitleSetter titleKey="routeTitles.login"><Login /></TitleSetter>} />
        <Route path="/registration" element={<TitleSetter titleKey="routeTitles.registration"><Registration /></TitleSetter>} />
        <Route path="/forgot-password" element={<TitleSetter titleKey="routeTitles.forgotPassword"><ForgotPassword /></TitleSetter>} />
    </>
);

export default publicRoutes;

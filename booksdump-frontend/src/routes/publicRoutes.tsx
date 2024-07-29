// src/routes/publicRoutes.tsx
import React from 'react';
import { Route } from 'react-router-dom';
import Login from '../components/Auth/Login';
import Registration from '../components/Auth/Registration';
import ForgotPassword from '../components/Auth/ForgotPassword';
import TitleSetter from '../components/common/TitleSetter';
import PublicRoute from "../components/PublicRoute";
import ChangePassword from "../components/Auth/ChangePassword";
import Activation from "../components/Auth/Activation";

const publicRoutes = (
    <Route path="/" element={<PublicRoute />}>
        <Route path="/login" element={<TitleSetter titleKey="routeTitles.login"><Login /></TitleSetter>} />
        <Route path="/registration" element={<TitleSetter titleKey="routeTitles.registration"><Registration /></TitleSetter>} />
        <Route path="/forgot-password" element={<TitleSetter titleKey="routeTitles.forgotPassword"><ForgotPassword /></TitleSetter>} />
        <Route path="/change-password/:token" element={<TitleSetter titleKey="routeTitles.forgotPassword"><ChangePassword /></TitleSetter>} />
        <Route path="/activate/:token" element={<TitleSetter titleKey="routeTitles.activation"><Activation /></TitleSetter>} />
    </Route>
);

export default publicRoutes;
// src/routes/privateRoutes.tsx
import React from 'react';
import { Route } from 'react-router-dom';
import PrivateRoute from '../components/PrivateRoute';
import BooksList from '../components/Userspace/BooksList';
import TitleSetter from '../components/common/TitleSetter';

const privateRoutes = (
    <>
        <Route path="/books/page/:page" element={<PrivateRoute element={<TitleSetter titleKey="routeTitles.books"><BooksList /></TitleSetter>} />} />
    </>
);

export default privateRoutes;

// src/routes/privateRoutes.tsx
import React from 'react';
import { Route } from 'react-router-dom';
import PrivateRoute from '../components/PrivateRoute';
import BooksList from '../components/Userspace/BooksList';
import TitleSetter from '../components/common/TitleSetter';

const privateRoutes = (
    <>
        <Route path="/books/page/:page" element={<PrivateRoute element={<TitleSetter titleKey="routeTitles.books"><BooksList /></TitleSetter>} />} />
        <Route path="/books/find/author/:id/:page" element={<PrivateRoute element={<TitleSetter titleKey="routeByAuthor.books"><BooksList /></TitleSetter>} />} />
        <Route path="/books/find/category/:id/:page" element={<PrivateRoute element={<TitleSetter titleKey="routeByCategory.books"><BooksList /></TitleSetter>} />} />
    </>
);

export default privateRoutes;

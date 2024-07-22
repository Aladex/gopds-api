// src/routes/privateRoutes.tsx
import React from 'react';
import { Route } from 'react-router-dom';
import PrivateRoute from '../components/PrivateRoute';
import BooksList from '../components/Userspace/BooksList';
import TitleSetter from '../components/common/TitleSetter';
import AuthorSearch from '../components/Userspace/AuthorSearch';

const privateRoutes = (
    <>
        <Route path="/books/page/:page" element={<PrivateRoute element={<TitleSetter titleKey="routeTitles.books"><BooksList /></TitleSetter>} />} />
        <Route path="/books/favorite/:page" element={<PrivateRoute element={<TitleSetter titleKey="favoriteTitles.books"><BooksList /></TitleSetter>} />} />
        <Route path="/books/find/author/:id/:page" element={<PrivateRoute element={<TitleSetter titleKey="routeByAuthor.books"><BooksList /></TitleSetter>} />} />
        <Route path="/books/find/category/:id/:page" element={<PrivateRoute element={<TitleSetter titleKey="routeByCategory.books"><BooksList /></TitleSetter>} />} />
        <Route path="/books/find/title/:title/:page" element={<PrivateRoute element={<TitleSetter titleKey="routeByBookName.books"><BooksList /></TitleSetter>} />} />
        <Route path="/authors/:author/:page" element={<PrivateRoute element={<TitleSetter titleKey="routeByAuthorName.books"><AuthorSearch /></TitleSetter>} />} />
    </>
);

export default privateRoutes;

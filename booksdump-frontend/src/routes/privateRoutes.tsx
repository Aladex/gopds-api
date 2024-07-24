import React from 'react';
import { Route } from 'react-router-dom';
import PrivateRoute from '../components/PrivateRoute';
import BooksList from '../components/Userspace/BooksList';
import TitleSetter from '../components/common/TitleSetter';
import AuthorSearch from '../components/Userspace/AuthorSearch';
import LayoutWithSearchBar from '../components/Layouts/LayoutWithSearchBar';

const privateRoutes = (
    <>
            <Route path="/" element={<PrivateRoute><LayoutWithSearchBar /></PrivateRoute>}>
                    <Route path="/books/page/:page" element={<TitleSetter titleKey="routeTitles.books"><BooksList /></TitleSetter>} />
                    <Route path="/books/favorite/:page" element={<TitleSetter titleKey="favoriteTitles.books"><BooksList /></TitleSetter>} />
                    <Route path="/books/find/author/:id/:page" element={<TitleSetter titleKey="routeByAuthor.books"><BooksList /></TitleSetter>} />
                    <Route path="/books/find/category/:id/:page" element={<TitleSetter titleKey="routeByCategory.books"><BooksList /></TitleSetter>} />
                    <Route path="/books/find/title/:title/:page" element={<TitleSetter titleKey="routeByBookName.books"><BooksList /></TitleSetter>} />
                    <Route path="/authors/:author/:page" element={<TitleSetter titleKey="routeByAuthorName.books"><AuthorSearch /></TitleSetter>} />
            </Route>
    </>
);

export default privateRoutes;

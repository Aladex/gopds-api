import React from 'react';
import { Route } from 'react-router-dom';
import PrivateRoute from '../components/PrivateRoute';
import BooksList from '../components/Userspace/BooksList';
import TitleSetter from '../components/common/TitleSetter';
import AuthorSearch from '../components/Userspace/AuthorSearch';
import OpdsInfo from "../components/Userspace/OpdsInfo";
import LayoutWithSearchBar from '../components/Layouts/LayoutWithSearchBar';
import CollectionsList from "../components/Userspace/CollectionsList";

const privateRoutes = (
    <>
        <Route path="/" element={<PrivateRoute><LayoutWithSearchBar /></PrivateRoute>}>
            <Route path="/books/page/:page" element={<TitleSetter titleKey="routeTitles.books"><BooksList /></TitleSetter>} />
            <Route path="/books/favorite/:page" element={<TitleSetter titleKey="favoriteTitles.books"><BooksList /></TitleSetter>} />
            <Route path="/books/users/favorites/:page" element={<TitleSetter titleKey="otherUserFavorites.books"><BooksList /></TitleSetter>} />
            <Route path="/books/find/author/:id/:page" element={<TitleSetter titleKey="routeByAuthor.books"><BooksList /></TitleSetter>} />
            <Route path="/books/find/category/:id/:page" element={<TitleSetter titleKey="routeByCategory.books"><BooksList /></TitleSetter>} />
            <Route path="/books/find/title/:title/:page" element={<TitleSetter titleKey="routeByBookName.books"><BooksList /></TitleSetter>} />
            <Route path="/books/find/collection/:id/:page" element={<TitleSetter titleKey="routeByCollection.books"><BooksList /></TitleSetter>} />
            <Route path="/authors/:author/:page" element={<TitleSetter titleKey="routeByAuthorName.books"><AuthorSearch /></TitleSetter>} />
            <Route path="/private-collections/:page" element={<TitleSetter titleKey="routeTitles.privateCollections"><CollectionsList /></TitleSetter>} />
            <Route path="/public-collections/:page" element={<TitleSetter titleKey="routeTitles.publicCollections"><CollectionsList /></TitleSetter>} />
        </Route>
        <Route path="/catalog" element={<PrivateRoute><OpdsInfo /></PrivateRoute>} />
    </>
);

export default privateRoutes;
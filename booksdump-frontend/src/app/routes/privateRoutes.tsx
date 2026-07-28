import React from 'react';
import { Route } from 'react-router';
import PrivateRoute from '@/app/PrivateRoute';
import BooksList from '@/features/catalogue/BooksList';
import TitleSetter from '@/app/TitleSetter';
import AuthorSearch from '@/features/catalogue/AuthorSearch';
import OpdsInfo from '@/features/opds/OpdsInfo';
import LayoutWithSearchBar from '@/app/LayoutWithSearchBar';
import CollectionsList from '@/features/collections/CollectionsList';
import CollectionView from '@/features/collections/CollectionView';

const privateRoutes = (
    <>
        <Route
            path="/"
            element={
                <PrivateRoute>
                    <LayoutWithSearchBar />
                </PrivateRoute>
            }
        >
            <Route
                path="/books/page/:page"
                element={
                    <TitleSetter titleKey="routeTitles.books">
                        <BooksList />
                    </TitleSetter>
                }
            />
            <Route
                path="/books/favorite/:page"
                element={
                    <TitleSetter titleKey="favoriteTitles.books">
                        <BooksList />
                    </TitleSetter>
                }
            />
            <Route
                path="/books/users/favorites/:page"
                element={
                    <TitleSetter titleKey="otherUserFavorites.books">
                        <BooksList />
                    </TitleSetter>
                }
            />
            <Route
                path="/books/find/author/:id/:page"
                element={
                    <TitleSetter titleKey="routeByAuthor.books">
                        <BooksList />
                    </TitleSetter>
                }
            />
            <Route
                path="/books/find/category/:id/:page"
                element={
                    <TitleSetter titleKey="routeByCategory.books">
                        <BooksList />
                    </TitleSetter>
                }
            />
            <Route
                path="/books/find/genre/:id/:page"
                element={
                    <TitleSetter titleKey="routeByGenre.books">
                        <BooksList />
                    </TitleSetter>
                }
            />
            <Route
                path="/books/find/title/:title/:page"
                element={
                    <TitleSetter titleKey="routeByBookName.books">
                        <BooksList />
                    </TitleSetter>
                }
            />
            <Route
                path="/authors/:author/:page"
                element={
                    <TitleSetter titleKey="routeByAuthorName.books">
                        <AuthorSearch />
                    </TitleSetter>
                }
            />
            <Route
                path="/collections"
                element={
                    <TitleSetter titleKey="publicCollections.title">
                        <CollectionsList />
                    </TitleSetter>
                }
            />
            <Route
                path="/collections/page/:page"
                element={
                    <TitleSetter titleKey="publicCollections.title">
                        <CollectionsList />
                    </TitleSetter>
                }
            />
            <Route
                path="/collections/:id/page/:page"
                element={
                    <TitleSetter titleKey="publicCollections.title">
                        <CollectionView />
                    </TitleSetter>
                }
            />
        </Route>
        <Route
            path="/catalog"
            element={
                <PrivateRoute>
                    <OpdsInfo />
                </PrivateRoute>
            }
        />
    </>
);

export default privateRoutes;

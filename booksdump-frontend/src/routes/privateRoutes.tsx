// src/routes/privateRoutes.tsx
import React from 'react';
import { Route } from 'react-router-dom';
import PrivateRoute from '../components/PrivateRoute';
import Userspace from '../components/Userspace/BooksList';
import Adminspace from '../components/Adminspace/AdminPanel';

const privateRoutes = (
    <Route>
        <Route path="books/page/:page" element={<PrivateRoute element={<Userspace />} />} />
        <Route path="admin/*" element={<PrivateRoute element={<Adminspace />} />} />
    </Route>
);

export default privateRoutes;

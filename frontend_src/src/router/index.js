import Vue from 'vue'
import VueRouter from 'vue-router'
import axios from 'axios'
import adminRoutes from "@/router/routes/admin";
import booksRoutes from "@/router/routes/books";
import publicRoutes from "@/router/routes/public";
import donateRoutes from "@/router/routes/donate";

Vue.use(VueRouter);

const router = new VueRouter({
    mode: 'history',
    // base: process.env.BASE_URL,
    base: '/',
    routes: [
        {
            path: '/',
            redirect: '/books'
        }
    ].concat(
        donateRoutes,
        publicRoutes,
        booksRoutes,
        adminRoutes
    )
});

router.afterEach((to) => {
    let bookSearch = "";
    if (to.params.title) {
        bookSearch = to.params.title
    }
    document.title = to.meta.title + bookSearch || 'Библиотека'
});

router.beforeEach((to, from, next) => {

    if (to.matched.some(record => record.meta.requiresAuth)) {
        // этот путь требует авторизации, проверяем залогинен ли
        // пользователь, и если нет, перенаправляем на страницу логина
        let token = localStorage.getItem('token');
        if (token == null) {
            next({
                path: '/login',
                query: {redirect: to.fullPath}
            })
        } else {
            axios.defaults.headers.common['Authorization'] = token;
            next()
        }
    } else if (to.matched.some(record => record.meta.checkAuth)) {
        let token = localStorage.getItem('token');
        if (token == null) {
            next()
        } else {
            axios.defaults.headers.common['Authorization'] = token;
            next({name: 'Books.BooksView'})
        }
    } else {
        next()
    }
});


export default router

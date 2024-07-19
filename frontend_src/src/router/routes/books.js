const booksRoutes = [
    {
        path: '/books/',
        name: 'Books',
        meta: {
            requiresAuth: true,
        },
        component: () => import(/* webpackChunkName: "books" */ '@/components/Books.vue'),
        children: [
            {
                path: "",
                name: "Books.BooksView",
                component: () => import(/* webpackChunkName: "books-view" */ '@/components/books/BooksView.vue'),
                redirect: '/books/page/1',
                meta: {
                    title: "Новые книги",
                }
            },
            {
                path: 'page/:page',
                name: 'page',
                component: () => import(/* webpackChunkName: "books-view" */ '@/components/books/BooksView.vue'),
                props: (route) => {
                    const page = Number.parseInt(route.params.page, 10);
                    if (Number.isNaN(page)) {
                        return 1
                    }
                    return {page, searchBar: true}
                },
                meta: {
                    title: "Новые книги",
                }
            },
            {
                path: 'find/books/:title/:page',
                name: 'findBook',
                component: () => import(/* webpackChunkName: "books-view" */ '@/components/books/BooksView.vue'),
                props: (route) => {
                    const page = Number.parseInt(route.params.page, 10);
                    if (Number.isNaN(page)) {
                        return 1
                    }
                    return {page, title: route.params.title, searchBar: true}
                },
                meta: {
                    title: "Поиск книги: ",
                }
            },
            {
                path: 'find/author/:author/:page',
                name: 'findByAuthor',
                component: () => import(/* webpackChunkName: "books-view" */ '@/components/books/BooksView.vue'),
                props: (route) => {
                    const page = Number.parseInt(route.params.page, 10);
                    if (Number.isNaN(page)) {
                        return 1
                    }
                    return {page, author: route.params.author, searchBar: true}
                },
                meta: {
                    title: "Книги по автору",
                }
            },
            {
                path: 'find/series/:series/:page',
                name: 'findBySeries',
                component: () => import(/* webpackChunkName: "books-view" */ '@/components/books/BooksView.vue'),
                props: (route) => {
                    const page = Number.parseInt(route.params.page, 10);
                    if (Number.isNaN(page)) {
                        return 1
                    }
                    return {page, series: route.params.series, searchBar: true}
                },
                meta: {
                    title: "Книги по серии",
                }
            },
            {
                path: 'authors/:title/:page',
                name: 'findAuthor',
                component: () => import(/* webpackChunkName: "authors" */ '@/components/Authors.vue'),
                props: (route) => {
                    const page = Number.parseInt(route.params.page, 10);
                    if (Number.isNaN(page)) {
                        return 1
                    }
                    return {page, author: route.params.title, searchBar: true}
                },
                meta: {
                    title: "Поиск автора: ",
                }
            },
            {
                path: 'unapproved/:page',
                name: 'Admin.Unapproved',
                component: () => import(/* webpackChunkName: "authors" */ '@/components/books/BooksView.vue'),
                props: (route) => {
                    const page = Number.parseInt(route.params.page, 10);
                    if (Number.isNaN(page)) {
                        return 1
                    }
                    return {page, unapproved: true, searchBar: true}
                },
                meta: {
                    title: "Неподтвержденные",
                }
            },
        ],

    },
    {
        path: '/upload/',
        name: 'BookUpload',
        component: () => import(/* webpackChunkName: "upload" */ '@/components/BookUpload.vue'),
        meta: {
            requiresAuth: true,
            title: "Добавить книгу",
        }
    },
]

export default booksRoutes
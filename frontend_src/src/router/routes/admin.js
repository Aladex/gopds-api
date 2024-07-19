import store from "@/store";

const adminArea = (to, from, next) => {
    function proceed() {
        if (!store.state.user.is_superuser) {
            next({
                path: '/404',
                query: {redirect: to.fullPath}
            })
        } else {
            next()
        }
    }

    if (store.state.loading) {
        store.watch(
            (state) => state.loading,
            (value) => {
                if (!value)
                    proceed()
            }
        )
    } else
        proceed()
};

const adminRoutes = [
    {
        path: '/admin',
        name: "Admin",
        redirect: '/admin/users',
        component: () => import(/* webpackChunkName: "admin" */ '@/components/Admin.vue'),
        children: [
            {
                path: "users",
                name: "Admin.Users",
                meta: {
                    title: "Управление пользователями",
                },
                component: () => import(/* webpackChunkName: "users" */ '@/components/admin/Users.vue'),
            },
            {
                path: "invites",
                name: "Admin.Invites",
                meta: {
                    title: "Управление инвайтами",
                },
                component: () => import(/* webpackChunkName: "invites" */ '@/components/admin/Invites.vue'),
            }
        ],
        beforeEnter: adminArea,
        meta: {
            requiresAuth: true,
        }
    },
]

export default adminRoutes
const donateRoutes = [
    {
        path: '/donate',
        name: 'Donate',
        component: () => import(/* webpackChunkName: "donate" */ '@/components/Donate.vue'),
        meta: {
            requiresAuth: true,
            title: "Задонатить",
        }
    },
    {
        path: '/catalog',
        name: 'Opds',
        component: () => import(/* webpackChunkName: "catalog" */ '@/components/Opds.vue'),
        meta: {
            requiresAuth: true,
            title: "OPDS",
        }
    },

];

export default donateRoutes
import Login from "@/components/auth/Login";
import Registration from "@/components/auth/Registration";
import ForgetForm from "@/components/auth/ForgetForm";
import Activation from "@/components/auth/Activation";
import Reset from "@/components/auth/Reset";
import NotFound from "@/components/errors/NotFound";

const publicRoutes = [

    {
        path: '/login',
        name: 'Login',
        component: Login,
        meta: {checkAuth: true, title: "Вход в библиотеку"}
    },
    {
        path: '/registration',
        name: 'Registration',
        component: Registration,
        meta: {checkAuth: true, title: "Секретная страница"}
    },
    {
        path: '/forget',
        name: 'ForgetForm',
        component: ForgetForm,
        meta: {checkAuth: true, title: "Я забыл"}
    },
    {
        path: '/activate/:token',
        name: 'Activation',
        component: Activation,
        meta: {checkAuth: true, title: "Активировать учетную запись"},
        props: (route) => ({token: route.params.token})
    },
    {
        path: '/change-password/:token',
        name: 'Reset',
        component: Reset,
        meta: {checkAuth: true, title: "Изменить пароль"},
        props: (route) => ({token: route.params.token})
    },
    {
        path: '/logout',
        name: 'logout',
    },
    {
        path: '*',
        name: 'NotFound',
        component: NotFound,
        meta: {title: "Страница не найдена"}
    },
]

export default publicRoutes
import Vue from 'vue'
import Vuex from 'vuex'
import axios from 'axios'
import router from '@/router/index'

Vue.use(Vuex);

export default new Vuex.Store({
    state: {
        user: {},
        langChange: false,
        loading: true,
        myPage: 1,
        length: 1,
        title: '',
        lang: {},
        fav: false,
        have_favs: true,
        langs: [],
        authorsBook: "",
        token: localStorage.getItem('token') || '',
        authError: false,
        username: '',
        status: '',
        searchItem: null,
        selectedSearch: {name: "book", title: "Поиск книги по названию"},
        searchVariants: [
            {name: "book", title: "Поиск книги по названию"},
            {name: "author", title: "Поиск автора"},
        ]
    },
    mutations: {
        setPage(state, payload) {
            state.myPage = payload
        },
        setLang(state, payload) {
            state.lang = payload
        },
        setFav(state, payload) {
            state.fav = payload
        },
        setHaveFavs(state, payload) {
            state.have_favs = payload
        },
        setLangs(state, payload) {
            state.langs = payload
        },
        setLangChange(state, payload) {
            state.langChange = payload
        },
        setTitle(state, payload) {
            state.title = payload
        },
        logout(state) {
            state.status = '';
            state.token = ''
        },
        changeErrorAuth(state, payload) {
            state.authError = payload
        },
        auth_request(state) {
            state.status = 'loading'
        },
        auth_success(state, token) {
            state.status = 'success';
            state.token = token
        },
        auth_error(state) {
            state.status = 'error'
        },
        searchSet(state, searchType) {
            state.selectedSearch = searchType
        },
        searchItem(state, item) {
            state.searchItem = item
        },
        authorsBook(state, item) {
            state.authorsBook = item
        },
        setUser(state, user) {
            state.status = 'success';
            state.user = user;
            state.loading = false;
            state.have_favs = user.have_favs;
        },
        setLength(state, length) {
            state.length = length;
        }
    },
    actions: {
        setPage({commit}, payload) {
            commit('setPage', payload)
        },
        setLang({commit}, payload) {
            commit('setLang', payload)
        },
        setLangChange({commit}, payload) {
            commit('setLangChange', payload)
        },
        setFav({commit}, payload) {
            commit('setFav', payload)
        },
        setHaveFavs({commit}, payload) {
            commit('setHaveFavs', payload)
        },
        setLangs({commit}, payload) {
            commit('setLangs', payload)
        },
        setTitle({commit}, payload) {
            commit('setTitle', payload)
        },
        getMe({commit}) {
            return new Promise((resolve, reject) => {
                axios(
                    {
                        url: process.env.VUE_APP_BACKEND_API_URL + 'api/books/self-user',
                        method: 'GET',
                        headers: {
                            "Authorization": localStorage.getItem('token')
                        }
                    }
                ).then(resp => {
                        const user = resp.data;
                        commit('setUser', user);
                        resolve(resp)
                    }
                ).catch(err => {
                        commit("logout");
                        router.push({
                            name: 'Login'
                        });
                        reject(err)
                    }
                )
            })
        },
        login({commit}, user) {
            return new Promise((resolve, reject) => {
                commit('auth_request');
                axios(
                    {
                        url: process.env.VUE_APP_BACKEND_API_URL + 'api/login',
                        data: user,
                        method: 'POST'
                    })
                    .then(resp => {
                        const user = resp.data;
                        const token = resp.data.token;
                        localStorage.setItem('token', token);
                        axios.defaults.headers.common['Authorization'] = token;
                        commit('auth_success', token);
                        commit('setUser', user);
                        resolve(resp)

                    })
                    .catch(err => {
                        commit('auth_error');
                        localStorage.removeItem('token');
                        reject(err)
                    })

            })
        },
        authChangeError({commit}, payload) {
            commit('changeErrorAuth', payload)
        },

        logout({commit}) {
            return new Promise((resolve) => {
                    commit('logout');
                    localStorage.removeItem('token');
                    axios(
                        {
                            url: process.env.VUE_APP_BACKEND_API_URL + 'api/logout',
                            method: "GET"
                        },
                    );
                    resolve()
                }
            )
        },
        dropSessions({commit}) {
            return new Promise((resolve) => {
                    commit('logout');
                    localStorage.removeItem('token');
                    axios(
                        {
                            url: process.env.VUE_APP_BACKEND_API_URL + 'api/drop-sessions',
                            method: "GET"
                        },
                    );
                    resolve()
                }
            )
        },
        searchSet({commit}, payload) {
            commit('searchSet', payload)
        },
        searchItem({commit}, payload) {
            commit('searchItem', payload)
        },
        authorsBook({commit}, payload) {
            commit('authorsBook', payload)
        },
        setLength({commit}, payload) {
            commit('setLength', payload)
        },
    },
    getters: {
        myPage: state => state.myPage,
        length: state => state.length,
        title: state => state.title,
        lang: state => state.lang,
        langChange: state => state.langChange,
        fav: state => state.fav,
        have_favs: state => state.have_favs,
        langs: state => state.langs,
        token: state => state.token,
        authError: state => state.authError,
        authStatus: state => state.status,
        searchVariants: state => state.searchVariants,
        selectedSearch: state => state.selectedSearch,
        searchItem: state => state.searchItem,
        authorsBook: state => state.authorsBook,
        isLoggedIn: state => !!state.token,
        user: state => state.user,
        loading: state => state.loading,
    },
    modules: {}
})

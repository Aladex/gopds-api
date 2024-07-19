import Vue from 'vue'
import App from './App.vue'
import store from './store'
import router from './router'
import vuetify from './plugins/vuetify';
import 'roboto-fontface/css/roboto/roboto-fontface.css'
import '@mdi/font/css/materialdesignicons.css'
import Axios from 'axios'
import VueClipboard from 'vue-clipboard2'
import DatetimePicker from 'vuetify-datetime-picker'

Vue.config.productionTip = false;
Vue.prototype.$http = Axios;
Vue.use(VueClipboard);
Vue.use(DatetimePicker);

Axios.interceptors.response.use(null, function (error) {
    if (error.response.status === 401) {
        localStorage.removeItem('token')
        store.dispatch('logout')
        router.push('/login')
    }
    return Promise.reject(error)
})

new Vue({
    store,
    router,
    vuetify,
    render: h => h(App),
}).$mount('#app');

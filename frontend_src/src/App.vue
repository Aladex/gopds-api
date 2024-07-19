<template>
    <v-app id="app">
        <v-system-bar color="red"
                      v-if="(this.$store.state.authError && myPath === 'Login')"
        >
            <span><b>Неправильный логин или пароль</b></span>
        </v-system-bar>
        <v-card
                color="grey lighten-4"
                v-if="isLoggedIn"
                flat
                tile
                height="50px"
        >
            <v-app-bar
                    color="primary"
                    dark
                    fixed
                    short
            ><v-app-bar-nav-icon v-show="mini" @click="drawer = true"></v-app-bar-nav-icon>
                <router-link
                        :to="{ name: 'Books.BooksView'}"
                >
                    <v-img
                            class="d-none d-md-block logo ml-n6"
                            contain
                            max-height="36"
                            max-width="36"
                            src="@/assets/logo.png"
                    ></v-img>
                </router-link>
                <v-toolbar-title class="d-none d-lg-flex">
                    <router-link
                            :to="{ name: 'Books.BooksView'}"
                            class="pointer pl-8"
                            tag="span"
                    >
                        Библиотека
                    </router-link>
                </v-toolbar-title>
                <span>
                </span>
                <v-spacer></v-spacer>
                <span>
                <v-tabs
                        background-color="primary"
                        class="d-none d-md-block"
                        right
                ><v-tabs-slider></v-tabs-slider>
                    <v-tab
                            :key="m.name"
                            :to="{ name: m.name}"
                            v-for="m in menu"

                    >{{ m.title }}</v-tab>
                </v-tabs>
                </span>

                <v-toolbar-items>
                    <v-btn
                            @click="openEdit = true"
                            class="d-none d-sm-block"
                            text
                    >{{ user.username }}
                    </v-btn>
                    <v-btn
                            @click="openEdit = true"
                            class="d-flex d-sm-none"
                            icon
                    >
                        <v-icon>mdi-account</v-icon>
                    </v-btn>

                    <v-btn
                            @click="logout"
                            icon
                    >
                        <v-icon>mdi-export</v-icon>
                    </v-btn>
                </v-toolbar-items>


            </v-app-bar>


        </v-card>
        <v-navigation-drawer
          v-model="drawer"
          temporary
          class="fixedDrawer"
      >
        <v-list
            nav
            dense

        >
          <v-list-item-group
              v-model="group"
              active-class="text--accent-4"
          >
            <v-list-item
                :key="m.name"
                :to="{ name: m.name }"
                v-for="m in menu"
                @click="drawer = false"
            >
              <v-list-item-icon>
                <v-icon>{{ m.icon }}</v-icon>
              </v-list-item-icon>
              <v-list-item-title>{{ m.title }}</v-list-item-title>
            </v-list-item>
          </v-list-item-group>
        </v-list>
      </v-navigation-drawer>
        <v-main>

            <router-view></router-view>
        </v-main>
        <back-to-top
                v-if="isLoggedIn"
        ></back-to-top>
        <self-user-edit-form
                :dialog="openEdit"
                :user="user"
                @closed="closedDialog"
        ></self-user-edit-form>

    </v-app>
</template>
<script>
    import BackToTop from "@/components/utils/BackToTop";
    import SelfUserEditForm from "@/components/utils/SelfUserEditForm";

    export default {
        components: {
            BackToTop,
            SelfUserEditForm
        },
        data() {
            return {
                drawer: false,
                openEdit: false,
                group: null,
            }
        },
        computed: {
            mini: {
              get() {
                return this.$vuetify.breakpoint.smAndDown;
              },
            },
            myPath: function () {
                return this.$route.name
            },
            isLoggedIn() {
                return this.$store.getters.isLoggedIn
            },
            user: {
                get() {
                    return this.$store.getters.user
                },
            },
            menu: function () {
                let menu = [
                    { name: 'Books.BooksView', title: "Книги", logo: "../assets/logo.png", icon: "mdi-home" },
                    { name: 'Opds', title: "OPDS", icon: "mdi-book" },
                    { name: 'Donate', title: "Донат", icon: "mdi-wallet" },
                ];
                if (this.user.is_superuser) {
                    menu.push({name: 'Admin', title: "Админ", icon: "mdi-tune"})
                }
                return menu
            }
        },
        methods: {
            closedDialog(value) {
                this.openEdit = value;
                this.$store.dispatch('getMe')
            },
            logout() {
                this.$store.dispatch('logout')
                    .then(() => {
                        this.$router.push('/login')
                    })
            },
        },
    }
</script>
<style>
    .pointer {
        cursor: pointer;
    }

    .disable-login-btn {
        pointer-events: none;
    }

    .logo {
        cursor: pointer;
        position: relative;
        left: 25px;
        bottom: 2px;
    }

    #app {
        background: linear-gradient(to right, rgb(245, 245, 245) 0%, rgb(209, 209, 209) 100%);
    }

    .cardColor {
        background-color: rgba(255, 255, 255, 0.85) !important;
        border-color: white !important;
    }
    .fixedDrawer {
      position: fixed;
    }
</style>

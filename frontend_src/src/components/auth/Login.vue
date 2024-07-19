<template>
    <v-container
            class="fill-height"
            fluid
    >
        <router-link
                :to="{ name: 'Registration'}"
                class="devito"
        ></router-link>
        <div class="books d-none d-lg-block"></div>
        <v-row
                align="center"
                justify="center"
        >
            <v-col
                    cols="12"
                    md="4"
                    sm="8"
            >
                <v-card class="elevation-12 cardColor"
                >
                    <v-toolbar
                            color="custom accent-4"
                            flat
                    >
                        <v-toolbar-title>Авторизация</v-toolbar-title>

                    </v-toolbar>
                    <v-card-text>
                        <v-form
                                ref="form"
                                v-model="valid"
                                validation

                        >
                            <v-text-field
                                    :rules="emailRules"
                                    @keyup.enter="login"
                                    label="Логин или почта"
                                    name="login"
                                    prepend-icon="mdi-account"
                                    type="text"
                                    v-model="email"
                            />

                            <v-text-field
                                    :rules="passwordRules"
                                    @keyup.enter="login"
                                    id="password"
                                    label="Пароль"
                                    name="password"
                                    prepend-icon="mdi-lock"
                                    type="password"
                                    v-model="password"
                            />
                        </v-form>
                    </v-card-text>
                    <v-card-actions>
                        <router-link
                                :to="{ name: 'ForgetForm'}"
                        >
                            <v-icon
                                    class="pointer"
                            >mdi-lock-question
                            </v-icon>
                        </router-link>
                        <v-spacer/>
                        <v-btn
                                :disabled="!valid"
                                @click="login"
                                color="custom accent-4"
                        >Войти
                        </v-btn>
                    </v-card-actions>
                </v-card>
            </v-col>
        </v-row>
    </v-container>
</template>

<script>
    export default {
        name: "Login",
        data() {
            return {
                email: "",
                password: "",
                valid: false,
                emailRules: [
                    v => !!v || 'Обязательное поле',
                ],
                passwordRules: [
                    v => !!v || 'Пароль тоже нужен',
                ],
                expand: false,
            }
        },

        methods: {
            login() {
                let username = this.email.trim();
                let password = this.password;
                this.$store.dispatch('login', {username, password})
                    .then(() => {
                        this.$store.dispatch('authChangeError', false);
                        this.$router.push('/')
                    })
                    .catch(() => {
                        this.$store.dispatch('authChangeError', true)
                    })
            }
        },
        mounted() {
            this.expand = true
        }

    }
</script>

<style scoped>
    .devito {
        width: 500px;
        height: 350px;
        position: absolute;
        bottom: 0;
        right: 0;
        background: url('../../assets/devito_back.png');
        background-size: cover;
    }

    .books {
        width: 300px;
        height: 350px;
        position: absolute;
        bottom: 0;
        left: 0;
        background: url('../../assets/books_back.png');
        background-size: cover;
    }


</style>
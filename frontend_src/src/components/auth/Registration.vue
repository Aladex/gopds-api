<template>
    <v-container
            class="fill-height"
            fluid
    >
        <div class="devito"></div>
        <div class="books d-none d-lg-block"></div>
        <v-row
                align="center"
                justify="center"
        >
            <v-col
                    cols="12"
                    lg="4"
                    md="4"
                    sm="8"
            >
                <v-row
                        align="center"
                        justify="center"
                        v-if="regError !== ''"
                >
                    <v-col
                            cols="12"
                            md="12"
                            sm="12"
                    >
                        <v-card
                                class="elevation-12"
                                color="warning"
                        >
                            <v-card-text>
                                <p>Ошибка: {{ regError }}</p>
                            </v-card-text>
                        </v-card>
                    </v-col>
                </v-row>
                <v-row
                        align="center"
                        justify="center"
                >
                    <v-col
                            cols="12"
                            md="12"
                            sm="12"
                    >
                        <v-card class="elevation-12 cardColor"
                        >
                            <v-toolbar
                                    color="custom accent-4"
                                    flat
                            >
                                <v-toolbar-title>Регистрация</v-toolbar-title>

                            </v-toolbar>
                            <v-card-text>
                                <v-form
                                        ref="form"
                                        v-model="valid"
                                        validation

                                >
                                    <v-text-field
                                            :rules="usernameRules"
                                            @keyup.enter="register"
                                            label="Логин"
                                            name="username"
                                            prepend-icon="mdi-account"
                                            type="text"
                                            v-model="username"
                                    />
                                    <v-text-field
                                            :rules="emailRules"
                                            @keyup.enter="register"
                                            label="Электронная почта"
                                            name="email"
                                            prepend-icon="mdi-email"
                                            type="text"
                                            v-model="email"
                                    />

                                    <v-text-field
                                            :rules="passwordRules"
                                            id="password"
                                            label="Пароль"
                                            name="password"
                                            prepend-icon="mdi-lock"
                                            type="password"
                                            v-model="password"
                                    />
                                    <v-text-field
                                            :rules="inviteRules"
                                            id="invite"
                                            label="Инвайт"
                                            name="invite"
                                            prepend-icon="mdi-help"
                                            type="text"
                                            v-model="invite"
                                    />

                                </v-form>
                            </v-card-text>
                            <v-card-actions>
                                <router-link
                                        :to="{ name: 'Login'}"
                                >
                                    <v-icon
                                            class="pointer"
                                    >mdi-arrow-left
                                    </v-icon>
                                </router-link>

                                <v-spacer/>
                                <v-btn
                                        :disabled="!valid"
                                        @click="register"
                                        color="custom accent-4"
                                >Тыц!
                                </v-btn>
                            </v-card-actions>
                        </v-card>
                    </v-col>
                </v-row>
            </v-col>
        </v-row>

    </v-container>
</template>

<script>
    export default {
        name: "Registration",
        data() {
            return {
                regError: "",
                username: "",
                email: "",
                password: "",
                invite: "",
                valid: false,
                inviteRules: [
                    v => !!v || 'Обязательное поле',
                ],
                usernameRules: [
                    v => !!v || 'Обязательное поле',
                    v => /^[a-z0-9A-Z-_]{3,20}$/.test(v) || 'Минимум три латинских символов или цифр'
                ],
                emailRules: [
                    v => !!v || 'Обязательное поле',
                    v => !v || /^\w+([.-]?\w+)*@\w+([.-]?\w+)*(\.\w{2,3})+$/.test(v) || 'Введи корректный e-mail'
                ],
                passwordRules: [
                    v => !!v || 'Обязательное поле',
                    v => (v.length > 7) || 'Пароль не менее 8 символов',
                ],
                expand: false,
            }
        },

        methods: {
            register() {
                let registerData = {
                    username: this.username.trim(),
                    email: this.email.trim(),
                    password: this.password,
                    invite: this.invite.trim()
                };
                this.$http({
                    url: process.env.VUE_APP_BACKEND_API_URL + 'api/register',
                    data: registerData,
                    method: 'POST'
                })
                    .then(() => {
                        this.$router.push("/login")
                    })
                    .catch(err => {
                        switch (err.response.data.message) {
                            case "bad_invite":
                                this.regError = "Такого инвайта нет. Сходи на лепру.";
                                break;
                            case "bad_form":
                                this.regError = "Что-то не так с формой";
                                break;
                            case "user_exists":
                                this.regError = "Такой пользователь уже есть";
                                break;
                            default:
                                this.regError = "Что-то пошло не так"
                        }
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
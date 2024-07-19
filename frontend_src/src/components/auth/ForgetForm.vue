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
                        v-if="resetError !== ''"
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
                                <p>Ошибка: {{ resetError }}</p>
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
                                <v-toolbar-title>Я не помню пароль, но помню свою почту</v-toolbar-title>

                            </v-toolbar>
                            <v-card-text>
                                <v-form
                                        ref="form"
                                        v-model="valid"
                                        validation

                                >
                                    <v-text-field
                                            :rules="emailRules"
                                            @keydown.enter.prevent="changePassword"
                                            label="Электронная почта"
                                            name="email"
                                            prepend-icon="mdi-email"
                                            type="text"
                                            v-model="email"
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
                                        @click="changePassword"
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
                resetError: "",
                email: "",
                valid: false,
                emailRules: [
                    v => !!v || 'Обязательное поле',
                    v => !v || /^\w+([.-]?\w+)*@\w+([.-]?\w+)*(\.\w{2,3})+$/.test(v) || 'Введи корректный e-mail'
                ],
                expand: false,
            }
        },

        methods: {
            changePassword() {
                let resetData = {
                    email: this.email,
                };
                this.$http({
                    url: process.env.VUE_APP_BACKEND_API_URL + 'api/change-request',
                    data: resetData,
                    method: 'POST'
                })
                    .then(() => {
                        this.$router.push("/login")
                    })
                    .catch(err => {
                        switch (err.response.data.message) {
                            case "bad_form":
                                this.resetError = "Что-то не так с формой";
                                break;
                            case "invalid_user":
                                this.resetError = "Пользователя с такой почтой нет";
                                break;
                            default:
                                this.resetError = "Что-то пошло не так"
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
<template>
    <v-container
            class="fill-height"
            fluid
    >
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
                        v-if="error !== ''"
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
                                <p>Ошибка: {{ error }}</p>
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
                                <v-toolbar-title>{{ message }}</v-toolbar-title>

                            </v-toolbar>
                            <v-card-text>
                                <v-form
                                        ref="form"
                                        v-model="valid"
                                        validation
                                >
                                    <v-text-field
                                            :rules="passwordRules"
                                            @keydown.enter.prevent="setNewPassword"
                                            id="password"
                                            label="Пароль"
                                            name="password"
                                            prepend-icon="mdi-lock"
                                            required
                                            type="password"
                                            v-model="password"
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
                                        @click="setNewPassword"
                                        color="custom accent-4"
                                >Войти
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
        props: ["token", "isRegister"],
        data() {
            return {
                error: "",
                valid: false,
                message: "",
                password: "",
                passwordRules: [
                    v => !!v || 'Обязательное поле',
                    v => (v.length > 7) || 'Пароль не менее 8 символов',
                ],
            }
        },
        name: "Reset",
        methods: {
            tokenValidation() {
                this.$http({
                    url: process.env.VUE_APP_BACKEND_API_URL + 'api/token',
                    data: {
                        token: this.token
                    },
                    method: 'POST'
                }).then(() => {
                    this.message = "Сброс пароля"
                }).catch(() => {
                    this.$router.push("/404")
                })
            },
            setNewPassword() {
                this.$http.post(
                    process.env.VUE_APP_BACKEND_API_URL + 'api/change-password',
                    {
                        password: this.password,
                        token: this.token
                    }
                ).then(() => {
                    this.$router.push("/login")
                }).catch(err => {
                    switch (err.response.data.message) {
                        case "bad_form":
                            this.error = "Что-то не так с формой";
                            break;
                        default:
                            this.error = "Что-то пошло не так"
                    }
                })
            }
        },
        mounted() {
            this.tokenValidation()
        }
    }
</script>

<style scoped>

</style>
<template>
    <div>
        <v-row justify="center">
            <v-dialog
                    @click:outside="onClose(false)"
                    max-width="600px"
                    persistent
                    v-model="dialog"
            >
                <v-card>
                    <v-card-title>
                        <span class="headline">Расскажи о себе, {{ user.username }}</span>
                    </v-card-title>
                    <v-card-text>
                        <v-row
                                justify="space-between"
                        >
                            <div
                                    @click="willChange = !willChange"
                                    class="pointer ml-3"
                            >
                                <small
                                        v-if="!willChange"
                                >Поменять пароль</small>
                                <small
                                        v-if="willChange"
                                >Скрыть форму пароля</small>
                            </div>

                            <span
                                    @click="dropSessions"
                                    class="pointer mr-3"
                            >
                            <small
                                    v-if="!willChange"
                            >Выйти со всех устройств</small>
                        </span>
                        </v-row>
                    </v-card-text>
                    <v-card-text>
                        <v-container>
                            <v-row>

                                <v-col cols="12"
                                       v-if="willChange">
                                    <v-text-field
                                            :error-messages="errorsText"
                                            label="Пароль"
                                            type="password"
                                            v-model="password"
                                    ></v-text-field>
                                </v-col>
                                <v-col cols="12"
                                       v-if="willChange">
                                    <v-text-field
                                            :disabled="this.password === ''"
                                            :error-messages="errorsNPText"
                                            label="Новый пароль"
                                            type="password"
                                            v-model="newPassword"
                                    ></v-text-field>
                                </v-col>
                                <v-col cols="12">
                                    <v-text-field
                                            label="Имя"
                                            v-model="user.first_name"
                                    ></v-text-field>
                                </v-col>
                                <v-col cols="12">
                                    <v-text-field
                                            label="Фамилия"
                                            v-model="user.last_name"
                                    ></v-text-field>
                                </v-col>
                            </v-row>
                        </v-container>
                    </v-card-text>
                    <v-card-actions>
                        <v-btn @click="onClose(false)" color="blue darken-1" text>Закрыть</v-btn>
                        <v-spacer></v-spacer>
                        <v-btn @click="userChange(user)" color="red darken-1" text>Изменить</v-btn>
                    </v-card-actions>
                </v-card>
            </v-dialog>
        </v-row>

    </div>
</template>

<script>
    export default {
        props: ["user", "dialog"],
        name: "SelfUserEditForm",
        data() {
            return {
                willChange: false,
                errorsText: "",
                errorsNPText: "",
                password: "",
                newPassword: "",
                valid: true,
            }
        },
        methods: {
            dropSessions() {
                this.$store.dispatch('dropSessions')
                    .then(() => {
                        this.onClose(false)
                        this.$router.push('/login')
                    })
            },
            newPasswordErrorText(np) {
                if (this.password !== "" && np === "") {
                    this.errorsNPText = "Обязательное поле"
                } else if (this.password === this.newPassword && this.password !== "") {
                    this.errorsNPText = "Новый пароль должен отличаться от старого"
                } else if (this.password !== "" && np.length < 8) {
                    this.errorsNPText = "Пароль должен быть больше 8 символов"
                } else {
                    this.errorsNPText = ""
                }
            },
            onClose(dialog) {
                this.willChange = false;
                this.$emit('closed', dialog)
            },
            userChange(user) {
                user.new_password = this.newPassword;
                user.password = this.password;


                this.$http({
                    url: process.env.VUE_APP_BACKEND_API_URL + 'api/books/change-me',
                    data: user,
                    method: 'POST'
                })
                    .then(() => {
                        this.$store.dispatch('getMe');
                        this.onClose(false)
                    })
                    .catch(err => {
                        switch (err.response.status) {
                            case 400:
                                this.errorsNPText = "Пароль должен быть больше 8 символов";
                                break;
                            case 403:
                                this.errorsText = "Неправильный пароль";
                                break
                        }
                    })

            }

        },
        watch: {
            willChange() {
                this.password = "";
                this.newPassword = "";
                this.errorsText = ""
            },
            newPassword(value) {
                this.newPasswordErrorText(value)
            },

        }
    }
</script>

<style scoped>

</style>
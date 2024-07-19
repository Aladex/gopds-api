<template>
    <div>
        <v-row justify="center">
            <v-dialog max-width="600px" persistent v-model="dialog">
                <v-card>
                    <v-card-title>
                        <span class="headline">Изменить пользователя</span>
                    </v-card-title>
                    <v-card-text>
                        <v-container>
                            <v-row>
                                <v-col cols="12">ID: {{ user.id }}</v-col>
                                <v-col cols="12">
                                    <v-text-field
                                            label="Имя пользователя"
                                            v-model="user.username"
                                    ></v-text-field>
                                </v-col>
                                <v-col cols="12">
                                    <v-text-field
                                            label="Пароль"
                                            type="password"
                                            v-model="newPassword"
                                    ></v-text-field>
                                </v-col>
                                <v-col cols="12">
                                    <v-text-field
                                            label="Почта"
                                            v-model="user.email"
                                    ></v-text-field>
                                </v-col>
                                <v-col cols="12">
                                    <v-text-field
                                            label="First Name"
                                            v-model="user.first_name"
                                    ></v-text-field>
                                </v-col>
                                <v-col cols="12">
                                    <v-text-field
                                            label="Last Name"
                                            v-model="user.last_name"
                                    ></v-text-field>
                                </v-col>
                              <v-col cols="12">
                                <v-text-field
                                    label="Токен"
                                    v-model="user.bot_token"
                                ></v-text-field>
                              </v-col>
                                <v-col cols="6">
                                    <v-checkbox
                                            label="Активен"
                                            v-model="user.active"
                                    ></v-checkbox>
                                </v-col>

                                <v-col cols="6">
                                    <v-checkbox
                                            label="Админ"
                                            v-model="user.is_superuser"
                                    ></v-checkbox>
                                </v-col>

                            </v-row>
                        </v-container>
                        <small>*indicates required field</small>
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
        name: "UserEditForm",
        data() {
            return {
                newPassword: ""
            }
        },
        methods: {
            onClose(dialog) {
                this.newPassword = "";
                this.$emit('closed', dialog)
            },
            userChange(user) {
                user.password = this.newPassword;
                let bodyChange = {
                    action: "update",
                    user: user
                };
                this.$http({
                    url: process.env.VUE_APP_BACKEND_API_URL + 'api/admin/user',
                    data: bodyChange,
                    method: 'POST'
                })
                    .then(() => {
                        this.onClose(false)
                    })
                    .catch(err => {
                        console.log(err)
                    })
            }

        }
    }
</script>

<style scoped>

</style>
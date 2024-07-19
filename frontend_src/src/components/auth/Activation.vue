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
                    md="4"
                    sm="8"
            >
                <v-card class="elevation-12"
                >
                    <v-card-title>Кажется, всё получилось</v-card-title>
                    <v-card-text>
                        <p>{{ message }}</p>
                        <p>
                            <router-link
                                    :to="{ name: 'Login'}"
                                    class="pointer"
                            >
                                Попробуем войти
                            </router-link>
                        </p>
                    </v-card-text>
                </v-card>
            </v-col>
        </v-row>

    </v-container>
</template>

<script>
    export default {
        props: ["token"],
        data() {
            return {
                message: "",
            }
        },
        name: "Activation",
        methods: {
            activateUser() {
                this.$http({
                    url: process.env.VUE_APP_BACKEND_API_URL + 'api/change-password',
                    data: {
                        token: this.token
                    },
                    method: 'POST'
                }).then(() => {
                    this.message = "Отлично, учетная запись активирована"
                }).catch(() => {
                    this.$router.push("/404")
                })
            }
        },
        mounted() {
            this.activateUser()
        }
    }
</script>

<style scoped>

</style>
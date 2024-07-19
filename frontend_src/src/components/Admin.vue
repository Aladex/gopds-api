<template>
    <v-container
            fluid
    >
        <v-row
                justify="center"
        >
            <v-col
                    cols="12"
                    lg="8"
                    md="10"
                    sm="10"
                    xs="10"
            >
                <v-card>
                    <v-tabs
                            background-color="transparent"
                            grow
                            v-model="tab"
                    >
                        <v-tab
                                :key="item.name"
                                :to="{ name: item.name}"
                                v-for="item in items"
                        >
                            {{ item.title }}
                        </v-tab>
                    </v-tabs>
                    <router-view></router-view>
                </v-card>
            </v-col>
        </v-row>
    </v-container>
</template>

<script>
    export default {
        name: "Admin",
        data() {
            return {
                tab: {name: 'Admin.Users', title: "Пользователи"},
                items: [
                    {name: 'Admin.Users', title: "Пользователи"},
                    {name: 'Admin.Invites', title: "Инвайты"},
                ]
            }
        },
        computed: {
            pageLocal: {
                get() {
                    return this.$store.getters.myPage
                },
                set(page) {
                    this.$store.dispatch('setPage', page)
                }
            },
            pagesLength: {
                get() {
                    return this.$store.getters.length
                },
                set(length) {
                    this.$store.dispatch('setLength', length)
                }
            },
        },
        methods: {
            toPage(page) {
                this.$store.dispatch('setPage', page)
            },
        },

    }
</script>

<style scoped>

</style>
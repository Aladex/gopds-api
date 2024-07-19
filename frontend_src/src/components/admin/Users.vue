<template>
    <v-container
            fluid
    >
        <v-card-title>
            <v-row>
                <v-col
                        cols="12"
                        lg="4"
                        md="4"
                        sm="4"
                        xs="4"
                >
                    <v-text-field
                            @click:append="searchUser"
                            @keyup.enter="searchUser"
                            append-icon="mdi-database-search"
                            hide-details
                            label="Search"
                            single-line
                            v-model="search"
                    ></v-text-field>
                </v-col>
            </v-row>
        </v-card-title>

        <v-data-table
                :headers="headers"
                :items="users"
                :items-per-page="itemsPerPage"
                :options.sync="options"
                class="elevation-1"
                hide-default-footer
        >
            <template v-slot:item.is_superuser="{ item }">
                <v-icon>{{ viewSU(item.is_superuser) }}</v-icon>
            </template>
            <template v-slot:item.active="{ item }">
                <v-icon>{{ viewSU(item.active) }}</v-icon>
            </template>
            <template v-slot:item.last_login="{ item }">
                {{ toHumanDate(item.last_login) }}
            </template>
            <template v-slot:item.date_joined="{ item }">
                {{ toHumanDate(item.date_joined) }}
            </template>
            <template v-slot:item.action="{ item }">
                <v-icon
                        @click="editUser(item)"
                >
                    mdi-pencil
                </v-icon>
            </template>
        </v-data-table>
        <pagination class="mt-6"></pagination>
        <user-edit-form
                :dialog="openEdit"
                :user="editable"
                @closed="closedDialog"
        ></user-edit-form>

    </v-container>
</template>

<script>
    import UserEditForm from "@/components/utils/UserEditForm";
    import Pagination from "@/components/utils/Pagination";

    export default {
        components: {
            UserEditForm,
            Pagination
        },
        name: "Users",
        data() {
            return {
                search: "",
                openEdit: false,
                editable: {},
                itemsPerPage: 50,
                options: {},
                users: [],
                headers: [
                    {
                        text: 'ID',
                        align: 'start',
                        sortable: false,
                        value: 'id',
                    },
                    {text: 'Пользователь', value: 'username', sortable: false},
                    {text: 'Активен', value: 'active', sortable: false},
                    {text: 'Суперпользователь', value: 'is_superuser', sortable: false},
                    {text: 'E-Mail', value: 'email', sortable: false},
                    {text: 'Последний логин', value: 'last_login'},
                    {text: 'Дата регистрации', value: 'date_joined'},
                    {text: 'Токен бота', value: 'bot_token'},
                    {text: 'ID чата', value: 'telegram_id'},
                    {text: 'Действия', value: 'action', sortable: false, align: 'right'}
                ],
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
            searchUser() {
                this.pageLocal = 1
                this.getUsers()
            },
            closedDialog(value) {
                this.openEdit = value
            },
            editUser(user) {
                this.openEdit = true;
                this.editable = user
            },
            viewSU(value) {
                if (value) {
                    return 'mdi-checkbox-marked-circle-outline'
                }
                return 'mdi-checkbox-blank-circle-outline'
            },
            toHumanDate(value) {
                return new Date(value).toLocaleDateString('ru-RU', {
                    year: 'numeric',
                    month: 'long',
                    day: 'numeric',
                    hour: 'numeric',
                    minute: 'numeric',
                })
            },
            getUsers() {
                let numberedPage = Number.parseInt(this.pageLocal, 10);
                let offset = numberedPage > 1 ? (numberedPage - 1) * 50 : 0;


                let requestBody = {
                    limit: 50,
                    offset: offset,
                    username: this.search,
                    order: this.options.sortBy[0],
                    desc: this.options.sortDesc[0]
                };

                this.$http
                    .post(`${process.env.VUE_APP_BACKEND_API_URL}api/admin/users`, requestBody)
                    .then(response => {
                        this.users = response.data.users;
                        this.pagesLength = response.data.length
                    })
                    .catch(err => {
                        switch (err.response.status) {

                            case 404:
                                this.$router.push("/404");
                                break
                        }
                    });
                window.scrollTo(0, 0)
            },
            toPage(page) {
                this.$store.dispatch('setPage', page)
            },
        },
        mounted() {
            this.pageLocal = 1;
            this.getUsers()
        },
        watch: {
            pageLocal() {
                this.getUsers()
            },
            options() {
                this.getUsers()
            }
        }
    }
</script>

<style scoped>

</style>
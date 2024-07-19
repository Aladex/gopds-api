<template>
    <v-container
            fluid
    >
        <v-row
                class="justify-end"
        >
            <v-icon
                    @click="editInvite({before_date: nowDate() }, false)"
                    class="mr-3 pointer"
            >mdi-plus
            </v-icon>
        </v-row>
        <v-row>
            <v-col
                    cols="12"
            >
                <v-data-table
                        :headers="headers"
                        :items="invites"
                        class="elevation-1"
                >
                    <template v-slot:item.action="{ item }">
                        <v-icon
                                @click="editInvite(item, true)"
                        >
                            mdi-pencil
                        </v-icon>
                        <v-icon
                                @click="inviteDelete(item)"
                        >
                            mdi-delete
                        </v-icon>
                    </template>
                    <template v-slot:item.before_date="{ item }">
                        {{ toHumanDate(item.before_date) }}
                    </template>
                </v-data-table>

            </v-col>
        </v-row>
        <invite
                :dialog="openEdit"
                :invite="editable"
                :isEdit=isEdit
                @closed="closedDialog"
        ></invite>
    </v-container>
</template>

<script>
    import Invite from "@/components/utils/Invite";

    export default {
        components: {
            Invite
        },
        name: "Invites",
        data() {
            return {
                editable: {},
                isEdit: true,
                openEdit: false,
                headers: [
                    {
                        text: 'ID',
                        align: 'start',
                        sortable: false,
                        value: 'id',
                    },
                    {text: 'Инвайт', value: 'invite', sortable: false},
                    {text: 'Истекает', value: 'before_date', sortable: false},
                    {text: 'Действия', value: 'action', sortable: false, align: 'right'}
                ],
                invites: []
            }
        },
        methods: {
            nowDate: () => {
                let date = new Date(Date.now());
                return date.toISOString()
            },
            inviteDelete(invite) {
                let bodyChange = {
                    action: "delete",
                    invite: invite
                };
                this.$http({
                    url: process.env.VUE_APP_BACKEND_API_URL + 'api/admin/invite',
                    data: bodyChange,
                    method: 'POST'
                })
                    .then(() => {
                        this.getInvites()
                    })
                    .catch(err => {
                        console.log(err)
                    })
            },
            editInvite(invite, isEdit) {
                this.openEdit = true;
                this.isEdit = isEdit;
                this.editable = invite
            },
            closedDialog(value) {
                this.openEdit = value
            },
            toHumanDate(value) {
                return new Date(value).toLocaleDateString('ru-RU', {
                    year: 'numeric',
                    month: 'long',
                    day: 'numeric',
                    hour: 'numeric',
                    minute: 'numeric',
                    second: 'numeric',
                })
            },
            getInvites() {
                this.$http
                    .get(`${process.env.VUE_APP_BACKEND_API_URL}api/admin/invites`)
                    .then(response => {
                        this.invites = response.data.result;

                    })
                    .catch(err => {
                        switch (err.response.status) {

                            case 404:
                                this.$router.push("/404");
                                break
                        }
                    });
            },
        },
        mounted() {
            this.getInvites()
        },
        watch: {
            openEdit() {
                this.getInvites()
            }
        }
    }
</script>

<style scoped>

</style>
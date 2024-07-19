<template>
    <div>
        <v-row justify="center">
            <v-dialog max-width="300px" persistent v-model="dialog">
                <v-card>
                    <v-card-title>
                        <span class="headline">{{ isEdit ? 'Изменить инвайт' : 'Добавить инвайт' }}</span>
                    </v-card-title>
                    <v-card-text>
                        <v-container>
                            <v-row>
                                <v-col cols="12" v-if="isEdit">ID: {{ invite.id }}</v-col>
                                <v-col cols="12">
                                    <v-text-field
                                            label="Инвайт"
                                            v-model="invite.invite"
                                    ></v-text-field>
                                </v-col>
                                <v-col cols="12">
                                    <v-datetime-picker
                                            :datePickerProps="calenderProps"
                                            :timePickerProps="timePicker"
                                            clear-text="очистить"
                                            dateFormat="dd-MM-yyyy"
                                            label="Время окончания"
                                            required
                                            v-model="dateInvite"
                                    >
                                        <template slot="dateIcon">
                                            <v-icon>mdi-calendar</v-icon>
                                        </template>
                                        <template slot="timeIcon">
                                            <v-icon>mdi-clock</v-icon>
                                        </template>
                                    </v-datetime-picker>
                                </v-col>
                            </v-row>
                        </v-container>
                    </v-card-text>
                    <v-card-actions>
                        <v-btn @click="onClose(false)" color="blue darken-1" text>Закрыть</v-btn>
                        <v-spacer></v-spacer>
                        <v-btn @click="inviteChange(invite)" color="red darken-1" text>{{ isEdit ? 'Изменить' :
                            'Добавить' }}
                        </v-btn>
                    </v-card-actions>
                </v-card>
            </v-dialog>
        </v-row>
    </div>
</template>

<script>
    export default {
        name: "Invite",
        props: ["invite", "dialog", "isEdit"],
        data() {
            return {
                calenderProps: {
                    locale: "ru-RU",
                    'first-day-of-week': 1
                },
                timePicker: {
                    format: "24hr"
                }
            }
        },
        computed: {
            dateInvite: {
                get() {
                    return new Date(this.invite.before_date)
                },
                set(date) {
                    this.invite.before_date = new Date(date).toISOString()
                }
            },
        },
        methods: {
            onClose(dialog) {
                this.$emit('closed', dialog)
            },
            inviteChange(invite) {
                let action = this.isEdit ? 'update' : 'create'
                let bodyChange = {
                    action: action,
                    invite: invite
                };
                this.$http({
                    url: process.env.VUE_APP_BACKEND_API_URL + 'api/admin/invite',
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
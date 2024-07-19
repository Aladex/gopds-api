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
                    <v-card-title>404</v-card-title>
                    <v-card-text>
                        <p>Такой страницы нет</p>
                        <p>Попробуйте перейти на стартовую или поискать книгу</p>
                    </v-card-text>
                </v-card>
            </v-col>
        </v-row>

    </v-container>
</template>

<script>
    export default {
        name: "NotFound"
    }
</script>

<style scoped>

</style>
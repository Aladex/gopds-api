<template>
    <v-row justify="center">
        <v-col
                class="mt-4"
                cols="10"
                lg="6"
                md="10"
                sm="10"
        >
            <v-row>
                <v-col
                        class="text-center"
                        cols="12"
                >
                    <h1>А работает OPDS?</h1>
                </v-col>
            </v-row>
            <v-row>
                <v-col
                        cols="12"
                >
                    <h3>Работает.</h3>
                    <p>Просто добавьте в вашу любимую читалку каталог https://booksdump.com/opds/</p>
                    <p>OPDS работает в тестовом режиме и иногда косячит например в FBReader. Но в целом пользоваться можно.</p>
                    <p>Обязательна авторизация в приложении.</p>
                    <p>В AlReader авторизация прописывается через двоеточие, например username:pass</p>
                    <p>В старых версиях приложений может не работать, потому что они не поддерживают TLS 1.2</p>
                    <p>Например на старых читалках PocketBook, в которых нет поддержки TLS 1.2, OPDS работать не будет.</p>
                    <p>В старых Onyx Boox выдает ошибку null. Поправить можно обновив прошивку.</p>
                </v-col>
            </v-row>
        </v-col>
    </v-row>
</template>

<script>
    export default {
        name: "Opds"
    }
</script>

<style scoped>

</style>
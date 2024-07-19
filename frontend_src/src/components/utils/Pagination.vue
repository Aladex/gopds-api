<template>
    <div
            class="text-center"
    >
        <v-pagination
                :length="pagesLength"
                :total-visible="6"
                @input="toPage(pageLocal)"
                v-model="pageLocal"

        ></v-pagination>
    </div>
</template>

<script>
    export default {
        name: "Pagination",
        computed: {
            lang: {
                get() {
                    return this.$store.getters.lang
                },
                set(lang) {
                    this.$store.dispatch('setLang', lang)
                }
            },
            fav: {
              get() {
                return this.$store.getters.fav
              },
              set(state) {
                this.$store.dispatch('setFav', state)
              }
            },
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
                this.$store.dispatch('setPage', page);
                let thisPath = this.$router.currentRoute;
                switch (thisPath.name) {
                    case "findBook":
                        this.$router.push(`/books/find/books/${thisPath.params.title}/${page}`);
                        break;
                    case "findAuthor":
                        this.$router.push(`/books/authors/${thisPath.params.title}/${page}`);
                        break;
                    case "findByAuthor":
                        this.$router.push(`/books/find/author/${thisPath.params.author}/${page}`);
                        break;
                    case "findBySeries":
                        this.$router.push(`/books/find/series/${thisPath.params.series}/${page}`);
                        break;
                    case "Admin.Users":
                        break;
                    default:
                        if (this.$route.path !== `/books/page/${page}`) {
                            this.$router.push(`/books/page/${page}`)
                        }
                        break;
                }
            },
        },
    }
</script>

<style scoped>

</style>
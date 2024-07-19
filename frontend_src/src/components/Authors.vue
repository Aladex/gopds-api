<template>
    <v-container
            fluid
    >
        <book-find
                v-if="searchBar"
        ></book-find>

        <items-not-found
                v-if="(authors.length === 0 && !loading)"
        ></items-not-found>
        <v-row
                justify="center"
                v-if="loading"
        >
            <v-col
                    :key="a"
                    cols="12"
                    lg="8"
                    md="10"
                    sm="10"
                    v-for="a in 8"
                    xs="10"
            >
                <v-skeleton-loader

                        boilerplate
                        class="mx-auto"
                        type="list-item"
                ></v-skeleton-loader>
            </v-col>
        </v-row>


        <div v-if="!loading && authors.length > 0">
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
                    <v-list subheader>
                        <v-subheader>Найденные авторы</v-subheader>

                        <v-list-item
                                :key="author.id"
                                @click="toAuthorPage(author.id)"
                                v-for="author in authors"
                        >
                            <v-list-item-content>
                                <v-list-item-title v-text="author.full_name"></v-list-item-title>
                            </v-list-item-content>

                        </v-list-item>
                    </v-list>
                </v-col>
            </v-row>

            <pagination></pagination>
        </div>
    </v-container>
</template>

<script>
    import BookFind from "@/components/utils/BookFind";
    import ItemsNotFound from "@/components/errors/ItemsNotFound";
    import Pagination from "@/components/utils/Pagination";


    export default {

        props: ["page", "title", "searchBar", "author"],
        name: "Authors",
        data() {
            return {
                authors: Array.from(Array(10).keys()),
                loading: true
            }
        },
        components: {
            BookFind,
            ItemsNotFound,
            Pagination
        },
        computed: {
            pagesLength: {
                get() {
                    return this.$store.getters.length
                },
                set(length) {
                    this.$store.dispatch('setLength', length)
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
        },
        methods: {
            toAuthorPage(authorID) {
                this.$router.push(`/books/find/author/${authorID}/1`)
            },
            toPage(page) {
                this.$store.dispatch('setPage', page);
                let thisPath = this.$router.currentRoute;
                this.$router.push(`/books/authors/${thisPath.params.title}/${page}`)
            },
            getAuthors() {
                this.loading = true;
                let numberedPage = Number.parseInt(this.pageLocal, 10);
                let offset = numberedPage > 1 ? (numberedPage - 1) * process.env.VUE_APP_ONPAGE : 0;
                let requestBody = {
                    limit: process.env.VUE_APP_ONPAGE,
                    offset: offset,
                    author: this.author
                };

                this.$http
                    .get(`${process.env.VUE_APP_BACKEND_API_URL}api/books/authors`, {params: requestBody})
                    .then(response => {
                        this.authors = response.data.authors;
                        this.pagesLength = response.data.length;
                        this.loading = false
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
            setThisPage(page) {
                this.$store.dispatch('setPage', page)
            },
        },
        mounted() {
            this.setThisPage(this.page);
            this.getAuthors()
        },
        watch: {
            page() {
                this.setThisPage(this.page);
                this.getAuthors()
            },
            author() {
                this.setThisPage(this.page);
                this.getAuthors()
            },
        }

    }
</script>

<style scoped>

</style>
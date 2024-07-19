<template>
    <v-row justify="center">
        <v-col
                class="mt-4 mb-2"
                cols="12"
                lg="8"
                md="10"
                sm="10"
        >
            <v-card>
                <v-card-text>
                    <v-row justify="start">

                        <v-col
                                cols="12"
                                lg="3"
                                md="12"
                                sm="12"
                        >
                            <v-select
                                    :disabled="fav"
                                    :hint="selectedSearch.name === 'authorsBook'? authorName : ''"
                                    :item-text="itemText"
                                    :items="selects"
                                    label="Категория поиска"
                                    persistent-hint
                                    return-object
                                    v-model="selectedSearch"
                            ></v-select>
                        </v-col>
                        <v-col
                                cols="12"
                                lg="3"
                                md="12"
                                sm="12"
                        >

                            <v-text-field
                                    :disabled="fav"
                                    clearable
                                    @click:clear="authorsBook = ''"
                                    @keyup.enter="findByTitle"
                                    hide-details
                                    label="Что ищем?"
                                    single-line
                                    v-model="searchItem"
                            >
                            </v-text-field>
                        </v-col>
                        <v-col
                                cols="6"
                                lg="3"
                                md="6"
                                sm="6"
                        >
                            <v-btn
                                    :disabled="fav"
                                    @click="findByTitle"
                                    class="search-btn"
                            >
                                Искать
                            </v-btn>
                        </v-col>
                        <v-col
                                cols="6"
                                lg="3"
                                md="6"
                                sm="6"
                        >
                            <v-row
                                    justify="end"
                            >
                                <v-col
                                        class="mt-3"
                                        cols="8"
                                        lg="8"
                                        md="8"
                                        sm="8"
                                >
                                    <v-select
                                            :item-text="langText"
                                            :items="langs"
                                            class="lang-selector"
                                            flat
                                            label="Язык"
                                            return-object
                                            v-model="lang"
                                            @input="userBooksLanguage()"
                                    ></v-select>

                                </v-col>
                                <v-col
                                    cols="4"
                                    lg="4"
                                    md="4"
                                    sm="4"
                                >
                                  <v-btn
                                      class="mt-4"
                                      icon
                                      :disabled="!have_favs"
                                      @click="fav = !fav"
                                  >
                                    <v-icon v-if="fav" large color="yellow darken-4">mdi-star-box</v-icon>
                                    <v-icon v-else large>mdi-star-box-outline</v-icon>
                                  </v-btn>
                                </v-col>
                            </v-row>
                        </v-col>
                    </v-row>
                </v-card-text>
            </v-card>
        </v-col>
    </v-row>
</template>

<script>
    export default {
        name: "BookFind",
        data() {
            return {
                authorName: "",
                openSelect: false,
                selects: [],
            }
        },
        computed: {
            user: {
              get() {
                return this.$store.getters.user
              },
            },
            myPath: function () {
                return this.$route.name
            },
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
              set(fav) {
                this.$store.dispatch('setFav', fav)
              }
            },
            have_favs: {
              get() {
                return this.$store.getters.have_favs
              },
              set(have_favs) {
                this.$store.dispatch('setHaveFavs', have_favs)
              }
            },
            langs: {
                get() {
                    return this.$store.getters.langs
                },
                set(langs) {
                    this.$store.dispatch('setLangs', langs)
                }
            },
            selectedSearch: {
                get() {
                    return this.$store.getters.selectedSearch
                },
                set(value) {
                    this.$store.dispatch("searchSet", value)
                }
            },
            searchItem: {
                get() {
                    return this.$store.getters.searchItem
                },
                set(value) {
                    this.$store.dispatch("searchItem", value)
                }
            },
            searchVariants: {
                set(value) {
                    this.$store.dispatch("searchVariants", value)
                },
                get() {
                    return this.$store.getters.searchVariants
                },
            },
            authorsBook: {
                set(value) {
                    this.$store.dispatch("authorsBook", value)
                },
                get() {
                    return this.$store.getters.authorsBook
                },
            }
        },
        methods: {
            langText: item => item.language,
            itemText: item => item.title,
            userBooksLanguage() {
              let newUser = this.user
              newUser.books_lang = this.lang.language
              this.$http({
                url: process.env.VUE_APP_BACKEND_API_URL + 'api/books/change-me',
                data: newUser,
                method: 'POST'
              })
                  .then(() => {
                    this.$store.dispatch('getMe');
                  })
                  .catch(err => {
                    switch (err.response.status) {
                      case 400:
                        this.errorsNPText = "Пароль должен быть больше 8 символов";
                        break;
                      case 403:
                        this.errorsText = "Неправильный пароль";
                        break
                    }
                  })

                  if (this.$route.path !== `/books/page/1`) {
                    this.$router.push(`/books/page/1`)
                  } else {
                    this.$store.dispatch('setLangChange', true)
                  }
            },
            findByTitle() {
                this.fav = false
                this.$store.dispatch('setPage', 1);
                this.$store.dispatch('setLength', 1);
                switch (this.selectedSearch.name) {
                    case "book":
                        if (this.searchItem === null) {
                          this.$router.push(`/books/page/1`)
                        } else {
                          this.$router.push(`/books/find/books/${this.searchItem}/1`);
                        }
                        break;
                    case "author":
                        if (this.searchItem === null) {
                          this.$router.push(`/books/page/1`)
                        } else {
                          this.$router.push(`/books/authors/${this.searchItem}/1`);
                        }
                        break;
                    case "authorsBook":
                        this.authorsBook = this.searchItem;
                        this.$router.push(`/books/find/author/${this.$route.params.author}/1`);
                        break;
                }
            },
            makeSelects(path) {
                if (path === "findByAuthor") {
                    this.authorName = "";
                    this.getAuthorName(this.$route.params.author);
                    this.searchItem = "";
                    this.authorsBook = "";
                    this.selects = this.searchVariants.slice();
                    this.selects.push(
                        {
                            name: "authorsBook",
                            title: "Поиск книги у автора",
                        }
                    );
                    this.selectedSearch = this.selects[2]
                } else {
                    this.selects = this.searchVariants.slice();
                    if (!this.selects.includes(this.selectedSearch)) {
                        this.selectedSearch = this.searchVariants[0]
                    }

                }
            },
            getAuthorName(id) {
                let authorId = Number.parseInt(id, 10);
                this.$http({
                    url: process.env.VUE_APP_BACKEND_API_URL + 'api/books/author',
                    data: {
                        author_id: authorId
                    },
                    method: 'POST'
                })
                    .then(response => {
                        this.authorName = response.data.full_name
                    })
            }
        },
        watch: {
            myPath(path) {
                this.makeSelects(path)
            },
            authorId(value) {
                if (value !== undefined) {
                  this.getAuthorName(value)
                }
            },
        },
        mounted() {
            this.makeSelects(this.myPath)
        }
    }
</script>

<style scoped>
    .search-btn {
        position: relative;
        top: 12px;
    }

    .lang-selector {
        position: relative;
        bottom: 12px;
    }
</style>
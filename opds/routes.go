package opds

import (
	"net/http"

	"gopds-api/services"

	"github.com/gin-gonic/gin"
)

// SetupOpdsRoutes sets up the opds routes. The search feeds go through the
// shared search service; navigation, new-books, collections and download
// handlers keep their existing direct paths. Every feed that lists books
// gives them their author lines from lines; nil shows the legacy authors.
// Entries reads what each listed book's entry shows beyond its row — its
// translators and the files of its card; nil shows neither.
//
// entities switches the feeds to the entity identity (authors.identity,
// design E §4.2): an author feed takes an entity UUID, an entry's <author>
// links to /opds/author/<uuid> — a feed of that author's books, served only
// then — and a legacy number is not found. nil keeps the legacy identity.
func SetupOpdsRoutes(
	r *gin.RouterGroup, search services.PublicSearch, lines *services.AuthorLines, entities AuthorEntityReader,
	entries BookEntries,
) {
	searchHandler := &SearchHandler{Search: search, AuthorLines: lines, Entities: entities != nil, Entries: entries}
	feeds := &Feeds{AuthorLines: lines, Entries: entries, Entities: entities}

	r.GET("/", func(c *gin.Context) { c.Redirect(http.StatusMovedPermanently, hrefNewBooks) })
	r.GET("/new/:page/:author", feeds.GetNewBooks)
	r.GET("/favorites/:page", feeds.GetNewBooks)
	if entities != nil {
		r.GET("/author/:author", feeds.GetNewBooks)
	}
	r.GET("/book/:id/variants", feeds.GetBookVariants)

	// Global search
	r.GET("/search", Search)
	r.GET("/books", searchHandler.Books)
	r.GET("/search-author", searchHandler.Authors)

	// Languages navigation
	r.GET("/languages", GetLanguages)
	r.GET("/lang/:lang", GetLanguageRoot)
	r.GET("/lang/:lang/books/:page", feeds.GetBooksByLanguage)
	r.GET("/lang/:lang/search", SearchByLanguage)
	r.GET("/lang/:lang/search-books", searchHandler.BooksByLanguage)
	r.GET("/lang/:lang/search-authors", searchHandler.AuthorsByLanguage)
	r.GET("/lang/:lang/author/:author/:page", feeds.GetAuthorBooksByLanguage)

	// Collections navigation
	r.GET("/collections/:page", GetCollections)
	r.GET("/collection/:id/:page", feeds.GetCollectionBooks)

	// Download
	r.GET("/get/:format/:id", DownloadBook)
}

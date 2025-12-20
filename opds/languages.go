package opds

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/opdsutils"
)

// Language names mapping
var langNames = map[string]string{
	"ru": "Русский",
	"en": "English",
	"uk": "Українська",
	"be": "Беларуская",
	"de": "Deutsch",
	"fr": "Français",
	"es": "Español",
	"it": "Italiano",
	"pl": "Polski",
	"cs": "Čeština",
	"bg": "Български",
	"sr": "Српски",
	"hr": "Hrvatski",
	"sk": "Slovenčina",
	"hu": "Magyar",
	"ro": "Română",
	"nl": "Nederlands",
	"pt": "Português",
	"sv": "Svenska",
	"da": "Dansk",
	"no": "Norsk",
	"fi": "Suomi",
	"el": "Ελληνικά",
	"tr": "Türkçe",
	"ar": "العربية",
	"he": "עברית",
	"zh": "中文",
	"ja": "日本語",
	"ko": "한국어",
}

func getLangName(code string) string {
	if name, ok := langNames[code]; ok {
		return name
	}
	return strings.ToUpper(code)
}

func langLinks(lang string) []opdsutils.Link {
	return []opdsutils.Link{
		{
			Href: "/opds",
			Rel:  "start",
			Type: "application/atom+xml;profile=opds-catalog",
		},
		{
			Href: fmt.Sprintf("/opds/lang/%s", lang),
			Rel:  "up",
			Type: "application/atom+xml;profile=opds-catalog",
		},
		{
			Href: "/opds-opensearch.xml",
			Rel:  "search",
			Type: "application/opensearchdescription+xml",
		},
		{
			Href: fmt.Sprintf("/opds/lang/%s/search?searchTerms={searchTerms}", lang),
			Rel:  "search",
			Type: "application/atom+xml",
		},
	}
}

// GetLanguages returns a list of available languages
func GetLanguages(c *gin.Context) {
	languages := database.GetLanguages()

	rootLinks := []opdsutils.Link{
		{
			Href: "/opds",
			Rel:  "start",
			Type: "application/atom+xml;profile=opds-catalog",
		},
		{
			Href: "/opds-opensearch.xml",
			Rel:  "search",
			Type: "application/opensearchdescription+xml",
		},
		{
			Href: "/opds/search?searchTerms={searchTerms}",
			Rel:  "search",
			Type: "application/atom+xml",
		},
	}

	feed := &opdsutils.Feed{
		Title:   "Книги по языкам",
		Id:      "tag:root:languages",
		Links:   rootLinks,
		Updated: time.Now(),
	}
	feed.Items = []*opdsutils.Item{}

	for _, lang := range languages {
		feed.Items = append(feed.Items, &opdsutils.Item{
			Title: fmt.Sprintf("%s (%d)", getLangName(lang.Lang), lang.LanguageCount),
			Link: []opdsutils.Link{
				{
					Href: fmt.Sprintf("/opds/lang/%s", lang.Lang),
					Type: "application/atom+xml;profile=opds-catalog",
				},
			},
			Id:      fmt.Sprintf("tag:lang:%s", lang.Lang),
			Updated: time.Now(),
			Content: fmt.Sprintf("Книги на языке: %s", getLangName(lang.Lang)),
		})
	}

	atom, err := feed.ToAtom()
	if err != nil {
		logging.Errorf("Error converting feed to Atom: %v", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
}

// GetLanguageRoot returns navigation page for a specific language
func GetLanguageRoot(c *gin.Context) {
	lang := c.Param("lang")
	langName := getLangName(lang)

	rootLinks := []opdsutils.Link{
		{
			Href: "/opds",
			Rel:  "start",
			Type: "application/atom+xml;profile=opds-catalog",
		},
		{
			Href: "/opds/languages",
			Rel:  "up",
			Type: "application/atom+xml;profile=opds-catalog",
		},
		{
			Href: "/opds-opensearch.xml",
			Rel:  "search",
			Type: "application/opensearchdescription+xml",
		},
		{
			Href: fmt.Sprintf("/opds/lang/%s/search?searchTerms={searchTerms}", lang),
			Rel:  "search",
			Type: "application/atom+xml",
		},
	}

	feed := &opdsutils.Feed{
		Title:   langName,
		Id:      fmt.Sprintf("tag:lang:%s:root", lang),
		Links:   rootLinks,
		Updated: time.Now(),
	}
	feed.Items = []*opdsutils.Item{
		{
			Title: "Все книги",
			Link: []opdsutils.Link{
				{
					Href: fmt.Sprintf("/opds/lang/%s/books/0", lang),
					Type: "application/atom+xml;profile=opds-catalog",
				},
			},
			Id:      fmt.Sprintf("tag:lang:%s:all", lang),
			Updated: time.Now(),
			Content: fmt.Sprintf("Все книги на языке %s", langName),
		},
		{
			Title: "Поиск книг",
			Link: []opdsutils.Link{
				{
					Href: fmt.Sprintf("/opds/lang/%s/search?searchTerms={searchTerms}", lang),
					Type: "application/atom+xml",
				},
			},
			Id:      fmt.Sprintf("tag:lang:%s:search", lang),
			Updated: time.Now(),
			Content: "Поиск книг и авторов",
		},
	}

	atom, err := feed.ToAtom()
	if err != nil {
		logging.Errorf("Error converting feed to Atom: %v", err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
}

// GetBooksByLanguage returns books filtered by language
func GetBooksByLanguage(c *gin.Context) {
	lang := c.Param("lang")
	pageNum, err := strconv.Atoi(c.Param("page"))
	if err != nil {
		pageNum = 0
	}

	userID := c.GetInt64("user_id")

	filters := models.BookFilters{
		Limit:  10,
		Offset: 0,
		Lang:   lang,
	}

	if pageNum > 0 {
		filters.Offset = pageNum * 10
	}

	books, tc, err := database.GetBooks(userID, filters)
	if err != nil {
		logging.Error(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	rootLinks := langLinks(lang)

	if hasNextPage(filters.Limit, pageNum, tc) {
		rootLinks = append(rootLinks, opdsutils.Link{
			Href: fmt.Sprintf("/opds/lang/%s/books/%d", lang, pageNum+1),
			Rel:  "next",
			Type: "application/atom+xml;profile=opds-catalog",
		})
	}

	feed := &opdsutils.Feed{
		Title:   fmt.Sprintf("Книги: %s", getLangName(lang)),
		Id:      fmt.Sprintf("tag:lang:%s:books:%d", lang, pageNum),
		Links:   rootLinks,
		Updated: time.Now(),
	}
	feed.Items = []*opdsutils.Item{}

	isKoreader := strings.Contains(c.GetHeader("User-Agent"), "KOReader")

	for _, book := range books {
		bookItem := opdsutils.CreateItem(book, isKoreader)
		feed.Items = append(feed.Items, &bookItem)
	}

	atom, err := feed.ToAtom()
	if err != nil {
		logging.Error(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
}

// SearchByLanguage returns search options for a language
func SearchByLanguage(c *gin.Context) {
	lang := c.Param("lang")
	searchTerms := c.Query("searchTerms")

	if searchTerms == "" {
		httputil.NewError(c, http.StatusBadRequest, errors.New("searchTerms parameter is required"))
		return
	}

	rootLinks := langLinks(lang)

	feed := &opdsutils.Feed{
		Title:   fmt.Sprintf("Поиск: %s", getLangName(lang)),
		Id:      fmt.Sprintf("tag:lang:%s:search", lang),
		Links:   rootLinks,
		Updated: time.Now(),
	}
	feed.Items = []*opdsutils.Item{
		{
			Title: "Поиск авторов",
			Link: []opdsutils.Link{
				{
					Href: fmt.Sprintf("/opds/lang/%s/search-authors?name=%s", lang, url.QueryEscape(searchTerms)),
					Type: "application/atom+xml;profile=opds-catalog",
				},
			},
			Id:      "tag:search:author",
			Updated: time.Now(),
			Content: "Поиск авторов по фамилии",
		},
		{
			Title: "Поиск книг",
			Link: []opdsutils.Link{
				{
					Href: fmt.Sprintf("/opds/lang/%s/search-books?title=%s", lang, url.QueryEscape(searchTerms)),
					Type: "application/atom+xml;profile=opds-catalog",
				},
			},
			Id:      "tag:search:book",
			Updated: time.Now(),
			Content: "Поиск книг по названию",
		},
	}

	atom, err := feed.ToAtom()
	if err != nil {
		logging.Errorf("Error converting feed to Atom: %v", err)
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
}

// SearchBooksByLanguage searches books by title within a language
func SearchBooksByLanguage(c *gin.Context) {
	lang := c.Param("lang")
	var filters OpdsBooksSearch
	userID := c.GetInt64("user_id")

	if err := c.ShouldBindWith(&filters, binding.Query); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
		return
	}

	dbFilters := models.BookFilters{
		Limit:  10,
		Offset: 0,
		Title:  filters.Title,
		Lang:   lang,
	}

	if filters.Page > 0 {
		dbFilters.Offset = filters.Page * 10
	}

	books, tc, err := database.GetBooks(userID, dbFilters)
	if err != nil {
		c.XML(500, err)
		return
	}

	if len(books) == 0 {
		c.Data(200, "application/atom+xml;charset=utf-8", []byte(notFound))
		return
	}

	rootLinks := langLinks(lang)

	if hasNextPage(dbFilters.Limit, filters.Page, tc) {
		rootLinks = append(rootLinks, opdsutils.Link{
			Href: fmt.Sprintf("/opds/lang/%s/search-books?title=%s&page=%d", lang, url.QueryEscape(filters.Title), filters.Page+1),
			Rel:  "next",
			Type: "application/atom+xml;profile=opds-catalog",
		})
	}

	feed := &opdsutils.Feed{
		Title:   fmt.Sprintf("Поиск книг: %s", getLangName(lang)),
		Id:      fmt.Sprintf("tag:lang:%s:search:books:%s:%d", lang, url.QueryEscape(filters.Title), filters.Page),
		Links:   rootLinks,
		Updated: time.Now(),
	}
	feed.Items = []*opdsutils.Item{}

	isKoreader := strings.Contains(c.GetHeader("User-Agent"), "KOReader")

	for _, book := range books {
		bookItem := opdsutils.CreateItem(book, isKoreader)
		feed.Items = append(feed.Items, &bookItem)
	}

	atom, err := feed.ToAtom()
	if err != nil {
		logging.Errorf("Error converting feed to Atom: %v", err)
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
}

// SearchAuthorsByLanguage searches authors who have books in a specific language
func SearchAuthorsByLanguage(c *gin.Context) {
	lang := c.Param("lang")
	var filters OpdsAuthorSearch

	if err := c.ShouldBindWith(&filters, binding.Query); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
		return
	}

	dbFilters := models.AuthorFilters{
		Limit:  10,
		Offset: 0,
		Author: filters.Name,
		Lang:   lang,
	}

	if filters.Page > 0 {
		dbFilters.Offset = filters.Page * 10
	}

	authors, tc, err := database.GetAuthors(dbFilters)
	if err != nil {
		c.XML(500, err)
		return
	}

	if len(authors) == 0 {
		c.Data(200, "application/atom+xml;charset=utf-8", []byte(notFound))
		return
	}

	rootLinks := langLinks(lang)

	if hasNextPage(dbFilters.Limit, filters.Page, tc) {
		rootLinks = append(rootLinks, opdsutils.Link{
			Href: fmt.Sprintf("/opds/lang/%s/search-authors?name=%s&page=%d", lang, url.QueryEscape(filters.Name), filters.Page+1),
			Rel:  "next",
			Type: "application/atom+xml;profile=opds-catalog",
		})
	}

	feed := &opdsutils.Feed{
		Title:   fmt.Sprintf("Поиск авторов: %s", getLangName(lang)),
		Id:      fmt.Sprintf("tag:lang:%s:search:authors:%s:%d", lang, url.QueryEscape(filters.Name), filters.Page),
		Links:   rootLinks,
		Updated: time.Now(),
	}
	feed.Items = []*opdsutils.Item{}

	for _, a := range authors {
		feed.Items = append(feed.Items, &opdsutils.Item{
			Title: a.FullName,
			Link: []opdsutils.Link{
				{
					Href: fmt.Sprintf("/opds/lang/%s/author/%d/0", lang, a.ID),
					Type: "application/atom+xml;profile=opds-catalog",
				},
			},
			Id:      fmt.Sprintf("tag:author:%d", a.ID),
			Updated: time.Now(),
			Content: a.FullName,
		})
	}

	atom, err := feed.ToAtom()
	if err != nil {
		logging.Errorf("Error converting feed to Atom: %v", err)
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
}

// GetAuthorBooksByLanguage returns books by author filtered by language
func GetAuthorBooksByLanguage(c *gin.Context) {
	lang := c.Param("lang")
	authorID, err := strconv.Atoi(c.Param("author"))
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid author id"))
		return
	}

	pageNum, err := strconv.Atoi(c.Param("page"))
	if err != nil {
		pageNum = 0
	}

	userID := c.GetInt64("user_id")

	filters := models.BookFilters{
		Limit:  10,
		Offset: 0,
		Lang:   lang,
		Author: authorID,
	}

	if pageNum > 0 {
		filters.Offset = pageNum * 10
	}

	books, tc, err := database.GetBooks(userID, filters)
	if err != nil {
		logging.Error(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	rootLinks := langLinks(lang)

	if hasNextPage(filters.Limit, pageNum, tc) {
		rootLinks = append(rootLinks, opdsutils.Link{
			Href: fmt.Sprintf("/opds/lang/%s/author/%d/%d", lang, authorID, pageNum+1),
			Rel:  "next",
			Type: "application/atom+xml;profile=opds-catalog",
		})
	}

	// Get author name for title
	authorName := "Автор"
	if len(books) > 0 {
		for _, a := range books[0].Authors {
			if int(a.ID) == authorID {
				authorName = a.FullName
				break
			}
		}
	}

	feed := &opdsutils.Feed{
		Title:   fmt.Sprintf("%s (%s)", authorName, getLangName(lang)),
		Id:      fmt.Sprintf("tag:lang:%s:author:%d:%d", lang, authorID, pageNum),
		Links:   rootLinks,
		Updated: time.Now(),
	}
	feed.Items = []*opdsutils.Item{}

	isKoreader := strings.Contains(c.GetHeader("User-Agent"), "KOReader")

	for _, book := range books {
		bookItem := opdsutils.CreateItem(book, isKoreader)
		feed.Items = append(feed.Items, &bookItem)
	}

	atom, err := feed.ToAtom()
	if err != nil {
		logging.Error(err)
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.Data(200, "application/atom+xml;charset=utf-8", []byte(atom))
}

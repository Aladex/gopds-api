package opds

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gopds-api/database"
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
					Href: fmt.Sprintf("/opds/lang/%s/0", lang.Lang),
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
			Href: "/opds/search?searchTerms={searchTerms}",
			Rel:  "search",
			Type: "application/atom+xml",
		},
	}

	if hasNextPage(filters.Limit, pageNum, tc) {
		rootLinks = append(rootLinks, opdsutils.Link{
			Href: fmt.Sprintf("/opds/lang/%s/%d", lang, pageNum+1),
			Rel:  "next",
			Type: "application/atom+xml;profile=opds-catalog",
		})
	}

	feed := &opdsutils.Feed{
		Title:   fmt.Sprintf("Книги: %s", getLangName(lang)),
		Id:      fmt.Sprintf("tag:lang:%s:%d", lang, pageNum),
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

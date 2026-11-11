package authortriage

import (
	"strings"

	"gopds-api/internal/authorentity"
	"gopds-api/internal/authornorm"
)

// placeholderPhrasesV1 are the search keys of displays that stand for no
// author, beside the normalizer's own vocabulary. Exact keys only: «Аноним
// X» is a person's handle.
var placeholderPhrasesV1 = setOf(
	"неизвестен автор", "неизвестный автор", "автор неизвестен", "неустановленный автор", "unknown",
	"unknown unknown", "unknown author", "anonymous", "аноним", "admin", "user", "guest", "автор", "без автора",
	"unbekannter autor", //nolint:misspell // a German placeholder phrase
	"невідомий автор", "неизвестен", "неизвестный", "автор не указан",
	"неизвестен неизвестен",
)

// collectivePhrasesV1 are the search keys of displays that name a collective
// the organization test does not see.
var collectivePhrasesV1 = setOf("коллектив авторов", "колектив авторів", "autoru kolektīvs", "народное творчество")

// placeholderKeys and placeholderPrefixes are the normalizer's placeholder
// vocabulary (authornorm.PlaceholderVocabulary).
var placeholderKeys, placeholderPrefixes = func() (map[string]bool, []string) {
	keys, prefixes := authornorm.PlaceholderVocabulary()
	return setOf(keys...), prefixes
}()

func setOf(words ...string) map[string]bool {
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}

// displayKey is a display's search key, outer parentheses dropped.
func displayKey(display string) string {
	return strings.TrimSpace(strings.Trim(authornorm.SearchKey(display), "()"))
}

// PlaceholderPhrase reports a display that stands for no author: its key is a
// key of the normalizer's placeholder vocabulary or of placeholderPhrasesV1,
// or it is one word that starts with a placeholder prefix («Неизвестного»).
// A longer name with a placeholder word in it is a name.
func PlaceholderPhrase(display string) bool {
	key := displayKey(display)
	if key == "" {
		return false
	}
	if placeholderKeys[key] || placeholderPhrasesV1[key] {
		return true
	}
	if strings.Contains(key, " ") {
		return false
	}
	for _, p := range placeholderPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// collectiveName reports the display of an organization or a collective
// phrase.
func collectiveName(display string) bool {
	return authorentity.OrganizationName(display) || collectivePhrasesV1[displayKey(display)]
}

// The lists of triage-v2's guards (guards.go), from the triage-v1 audit's
// error shapes. Words are compared lower-case; stems as word prefixes of a
// display's search key.

// personRoles are the roles a file writes beside a person's name, never a
// name of their own («художник», «ed.»).
var personRoles = setOf("художник", "художница", "иллюстратор", "редактор", "ред", "составитель", "составители",
	"сост", "переводчик", "переводчица", "пер", "ed", "eds", "editor", "editors", "ill", "illus", "illustrator",
	"transl", "translator", "comp", "compiler", "hrsg", "übers")

// clericalTitles are the clerical titles and honorifics a person's name
// keeps in its prefix, never as its given or family name («Иеромонах
// Пантелеимон»).
var clericalTitles = setOf("иеромонах", "иером", "монах", "монахиня", "митрополит", "митр", "епископ", "еп",
	"архиепископ", "архиеп", "патриарх", "протоиерей", "прот", "священник", "свящ", "иерей", "архимандрит",
	"игумен", "игумения", "святитель", "свт", "преподобный", "прп", "святой", "схиархимандрит", "схиигумен",
	"иеросхимонах", "протодиакон", "диакон", "протопресвитер")

// genreWords and genreStems are the genres and kinds of book a list of
// them names («Эпосы, мифы, легенды и сказания», «Epic, legends and tales»,
// «[ сборник ]»).
var (
	genreWords = setOf("миф", "сказ", "сказы", "tale", "myth", "saga", "fable")
	genreStems = []string{"эпос", "эпич", "мифы", "мифов", "мифолог", "легенд", "сказани", "сказк", "предани",
		"былин", "притч", "басни", "пословиц", "поговорк", "сборник", "фольклор", "tales", "legend", "myths",
		"mytholog", "epic", "sagas", "fables", "folktale", "folklore"}
)

// conjunctions join the two names of a pair or the two titles of one
// person.
var conjunctions = setOf("и", "і", "та", "and", "&", "und", "et")

// titleNouns are the titles one person carries in pairs («Давид Царь и
// Пророк»); landSuffixes end the adjectives of a land («Христофор
// Греческий и Датский»).
var (
	titleNouns = setOf("царь", "царица", "царевич", "царевна", "пророк", "король", "королева", "князь", "княгиня",
		"император", "императрица", "принц", "принцесса", "апостол", "великий", "великая", "святой", "мудрый",
		"патриарх", "герцог", "герцогиня", "граф", "графиня")
	landSuffixes = []string{"ский", "ская", "цкий", "цкая", "ской"}
)

// pairWords are the lower-case words a pair of names writes beside the
// conjunction: «и др.», «и другие», «братья Гримм», the particles of a
// family name.
var pairWords = setOf("др", "другие", "others", "братья", "сестры", "сёстры", "brothers", "sisters", "de", "da", "del", "di", "du",
	"la", "le", "van", "von", "der", "den", "ten", "ter", "zu", "bin", "ibn", "ben", "al", "el", "де", "да",
	"дель", "ди", "дю", "ла", "ле", "ван", "фон", "дер", "тер", "цу", "бен", "бин", "ибн", "аль", "эль")

// stemPublisher is a publisher's word, both a collective's mark and a
// handle that is no person.
const stemPublisher = "издательств"

// collectiveStems mark a collective the organization test does not read.
var collectiveStems = []string{"коллектив", "колектив", "коллетив", "литагент", "редакц", "редколл",
	stemPublisher}

// handleStems are the one-word names of organizations, magazines, titles
// and placeholders a kept handle must not be («Компьютерра», «РПЦ»,
// «Bībele», «пользователь», «Терра»).
var handleStems = []string{"журнал", "газет", "альманах", stemPublisher, "церк", "рпц", "компьютерр",
	"библи", "bībel", "bibel", "bible", "пользовател", "терра"}

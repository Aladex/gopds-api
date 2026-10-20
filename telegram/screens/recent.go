package screens

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/ru"
)

// The recent sends (plan.md P1-3, concept S-RECENT, S-RECENT-SETTINGS and
// S-CLEAR-RECENT): books Telegram confirmed it delivered, to send again.
// It is never called reading: a confirmed send proves nothing more.
const (
	ButtonRecent       = "Недавние отправки"
	ButtonToRecent     = "← К недавним"
	ButtonKeepRecent   = "Сохранять"
	ButtonNoRecent     = "Не сохранять"
	ButtonClearRecent  = "Очистить список"
	CopyRecentCleared  = "Список недавних отправок очищен"
	CopyRecentKept     = "Недавние отправки сохраняются"
	CopyRecentNotKept  = "Недавние отправки не сохраняются"
	copyRecentLimits   = "Последние 50 книг за 90 дней. Время — по UTC."
	copyRecentGoneBook = "Книга больше недоступна"
	// recentTimeLayout is a send's date and time, in UTC: the library knows
	// no reader's time zone, and does not guess Telegram's.
	recentTimeLayout = "02.01.2006, 15:04 UTC"
	// styleDanger is Telegram's style of a button that destroys something.
	styleDanger = "danger"
)

// RecentItem is one recent send: the book as it is now (nil when it is gone
// from the catalog), the format it was last sent in, and when.
type RecentItem struct {
	Book   *models.Book
	BookID int64
	Format string
	SentAt time.Time
}

// RecentKeys are S-RECENT's envelopes.
type RecentKeys struct {
	Page   func(page int) *cbv2.Envelope
	Resend func(bookID int64, format string) *cbv2.Envelope
	Open   func(bookID int64) *cbv2.Envelope
	Home   *cbv2.Envelope
}

// RecentScreen is one page of S-RECENT. keep is whether new sends are kept:
// an empty list says so when they are not.
func RecentScreen(items []RecentItem, total, page int, keep bool, k *RecentKeys) Screen {
	if total == 0 {
		blocks := []Block{Title{Text: "Здесь появятся отправленные книги"}, Gap{},
			Line{Text: "Когда бот успешно пришлёт файл, его можно будет быстро отправить снова."}}
		if !keep {
			blocks = append(blocks, Line{Text: "Сейчас отправки не сохраняются — это меняется в настройках."})
		}
		return Screen{Doc: Doc{Blocks: blocks}, Keys: [][]Button{{{Text: ButtonHome, Env: k.Home}}}}
	}
	blocks := []Block{Title{Text: ButtonRecent}, Line{Text: copyRecentLimits}, Gap{}}
	var keys [][]Button
	for i := range items {
		it := &items[i]
		n := strconv.Itoa(page*PageSize + i + 1)
		format := strings.ToUpper(it.Format)
		if it.Book == nil {
			blocks = append(blocks, Line{Text: n + ". " + copyRecentGoneBook})
			continue
		}
		title := it.Book.Title
		if a := Authors(it.Book, listAuthors); a != "" {
			title += " — " + a
		}
		blocks = append(blocks,
			Line{Text: n + ". " + ru.Truncate(title, listLineRunes)},
			Line{Text: "   " + format + " · " + it.SentAt.UTC().Format(recentTimeLayout)})
		keys = append(keys, []Button{
			{Text: n + ". Отправить " + format, Env: k.Resend(it.BookID, it.Format)},
			{Text: "О книге " + n, Env: k.Open(it.BookID)},
		})
	}
	pages := Pages(total)
	if total > PageSize {
		blocks = append(blocks, Gap{}, Line{Text: fmt.Sprintf("Страница %d из %d", page+1, pages)})
	}
	if row := pagingRow(page, pages, k.Page); len(row) > 0 {
		keys = append(keys, row)
	}
	return Screen{Doc: Doc{Blocks: blocks}, Keys: append(keys, []Button{{Text: ButtonHome, Env: k.Home}}),
		Rich: richRecent(items, total, page, k)}
}

// RecentName is the keeping of recent sends in words, for the settings.
func RecentName(keep bool) string {
	if keep {
		return "сохранять"
	}
	return "не сохранять"
}

// RecentSettingsKeys are S-RECENT-SETTINGS' envelopes.
type RecentSettingsKeys struct {
	Keep  func(keep bool) *cbv2.Envelope
	Clear *cbv2.Envelope
	Back  *cbv2.Envelope
}

// RecentSettingsScreen is S-RECENT-SETTINGS. Not keeping does not pretend to
// clear: clearing is its own button.
func RecentSettingsScreen(keep bool, k *RecentSettingsKeys) Screen {
	on, off := ButtonKeepRecent, ButtonNoRecent
	state := "Сохраняются последние 50 книг за 90 дней."
	if keep {
		on = "✓ " + on
	} else {
		off = "✓ " + off
		state = "Новые отправки не сохраняются. Уже сохранённые остаются, пока их не очистить."
	}
	return Screen{Doc: Doc{Blocks: []Block{
		Title{Text: ButtonRecent}, Gap{},
		Line{Text: state},
		Line{Text: "Файлы в чате Telegram хранятся отдельно."},
	}}, Keys: [][]Button{
		{{Text: on, Env: k.Keep(true)}},
		{{Text: off, Env: k.Keep(false)}},
		{{Text: ButtonClearRecent, Env: k.Clear}},
		{{Text: ButtonToSettings, Env: k.Back}},
	}}
}

// ClearRecentScreen is S-CLEAR-RECENT: the one destructive confirmation,
// styled danger.
func ClearRecentScreen(confirm, cancel *cbv2.Envelope) Screen {
	return Screen{Doc: Doc{Blocks: []Block{
		Title{Text: "Очистить недавние отправки?"}, Gap{},
		Line{Text: "Список в библиотеке будет удалён."},
		Line{Text: "Файлы в чате Telegram останутся."},
	}}, Keys: [][]Button{
		{{Text: ButtonClearRecent, Env: confirm, Style: styleDanger}},
		{{Text: ButtonCancel, Env: cancel}},
	}}
}

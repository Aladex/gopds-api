package screens

import (
	"strings"

	"gopds-api/config"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/ru"
)

// The settings' words (plan-ui.md §1.9).
const (
	ButtonToSettings  = "← К настройкам"
	ButtonInline      = "Подсказки при поиске"
	ButtonDonate      = "Поддержать проект"
	ButtonOpenFather  = "Открыть @BotFather"
	ButtonCopyInline  = "Скопировать /setinline"
	ButtonStyleRich   = "Расширенный"
	ButtonStylePlain  = "Простой"
	botFatherURL      = "https://t.me/BotFather"
	setInlineCommand  = "/setinline"
	CardStyleRichName = "расширенный"
	CardStylePlainNm  = "простой"
	// donateLink is a donation method that is somewhere to go.
	donateLink = "link"
)

// SettingsKeys are the settings' envelopes; Donate is nil without donation
// methods.
type SettingsKeys struct {
	Format, Language, Style, Inline, Recent, Donate, Home *cbv2.Envelope
	// KindleURL is the site's Kindle setup; "" leaves the button out.
	KindleURL string
}

// StyleName is a card style in words.
func StyleName(plain bool) string {
	if plain {
		return CardStylePlainNm
	}
	return CardStyleRichName
}

// SettingsScreen is S-SETTINGS. kindle is the account's Kindle address, ""
// when none is set; it is set on the site.
func SettingsScreen(format, lang string, plain, inline, keepRecent bool, kindle string, k *SettingsKeys) Screen {
	language := allLanguages
	if lang != "" {
		language = ru.Language(lang)
	}
	format = strings.ToUpper(format)
	keys := [][]Button{
		{{Text: "Основной формат: " + format, Env: k.Format}},
		{{Text: "Язык поиска: " + language, Env: k.Language}},
		{{Text: "Вид карточек: " + StyleName(plain), Env: k.Style}},
		{{Text: ButtonInline, Env: k.Inline}},
		{{Text: ButtonRecent, Env: k.Recent}},
	}
	if k.KindleURL != "" {
		keys = append(keys, []Button{{Text: ButtonKindleSetup, URL: k.KindleURL}})
	}
	if k.Donate != nil {
		keys = append(keys, []Button{{Text: ButtonDonate, Env: k.Donate}})
	}
	return Screen{Doc: Doc{Blocks: []Block{
		Title{Text: "Настройки"}, Gap{},
		Line{Text: "Основной формат: " + format},
		Line{Text: "Язык поиска: " + language},
		Line{Text: "Вид карточек: " + StyleName(plain)},
		Line{Text: "Подсказки при поиске: " + inlineName(inline)},
		Line{Text: "Недавние отправки: " + RecentName(keepRecent)},
		Line{Text: "Kindle: " + kindleName(kindle)},
		Gap{},
		Line{Text: CopyStartCommand},
	}}, Keys: append(keys, []Button{{Text: ButtonHome, Env: k.Home}})}
}

// CopyStartCommand is the bot's one command, told once, in the settings.
const CopyStartCommand = "/start — главное меню"

// inlineName is inline mode's state in words.
func inlineName(on bool) string {
	if on {
		return "включены"
	}
	return "выключены"
}

// FormatScreen is S-FORMAT.
func FormatScreen(current string, set func(string) *cbv2.Envelope, back *cbv2.Envelope) Screen {
	label := func(f string) string {
		if f == current {
			return "✓ " + strings.ToUpper(f)
		}
		return strings.ToUpper(f)
	}
	return Screen{Doc: Doc{Blocks: []Block{
		Title{Text: "Основной формат"}, Gap{},
		Line{Text: "Он будет первым в карточке книги. Другие форматы останутся рядом."},
	}}, Keys: [][]Button{
		{{Text: label(FormatEPUB), Env: set(FormatEPUB)}, {Text: label(formatFB2), Env: set(formatFB2)}},
		{{Text: label(formatMOBI), Env: set(formatMOBI)}, {Text: label(formatZIP), Env: set(formatZIP)}},
		{{Text: ButtonToSettings, Env: back}},
	}}
}

// CardStyleScreen is S-CARD-STYLE.
func CardStyleScreen(plain bool, set func(plain bool) *cbv2.Envelope, back *cbv2.Envelope) Screen {
	rich, simple := ButtonStyleRich, ButtonStylePlain
	if plain {
		simple = "✓ " + simple
	} else {
		rich = "✓ " + rich
	}
	return Screen{Doc: Doc{Blocks: []Block{
		Title{Text: "Вид карточек"}, Gap{},
		Line{Text: "Расширенный — обложка, таблица и сворачиваемая аннотация в одном сообщении."},
		Line{Text: "Простой — обычный текст. Подходит, если карточка показывается неправильно."},
	}}, Keys: [][]Button{
		{{Text: rich, Env: set(false)}},
		{{Text: simple, Env: set(true)}},
		{{Text: ButtonToSettings, Env: back}},
	}}
}

// Code is text shown as code: a command inside it is not a tappable command.
type Code struct{ Prefix, Code, Suffix string }

// InlineState is what S-INLINE knows about the bot's inline mode.
type InlineState uint8

// The states of inline mode.
const (
	InlineOff InlineState = iota
	InlineOn
	// InlineJustOn is «Проверить» finding it on.
	InlineJustOn
)

// The inline instructions' words (plan-ui.md §1.9).
const (
	ButtonCheckInline = "Проверить"
	CopyInlineNotYet  = "Пока не включено. Проверь шаги выше."
	CopyInlineJustOn  = "Подсказки включены. Кнопка «Найти с подсказками» появилась на главной."
)

// InlineKeys are S-INLINE's envelopes.
type InlineKeys struct {
	Check, Back, Home *cbv2.Envelope
}

// InlineScreen is S-INLINE: how to turn inline mode on in BotFather, or that
// it is on.
func InlineScreen(botUsername string, state InlineState, k *InlineKeys) Screen {
	bot := "@" + botUsername
	if botUsername == "" {
		bot = "своего бота"
	}
	switch state {
	case InlineJustOn:
		return Screen{Doc: Doc{Blocks: []Block{Line{Text: CopyInlineJustOn}}}, Keys: [][]Button{{{Text: ButtonHome, Env: k.Home}}}}
	case InlineOn:
		return Screen{Doc: Doc{Blocks: []Block{
			Title{Text: "Подсказки при поиске включены"}, Gap{},
			Line{Text: "Нажми «Найти с подсказками» на главной или набери " + bot + " и название в поле ввода."},
		}}, Keys: [][]Button{{{Text: ButtonToSettings, Env: k.Back}}}}
	case InlineOff:
	}
	return Screen{Doc: Doc{Blocks: []Block{
		Title{Text: "Подсказки при поиске"}, Gap{},
		Line{Text: "Можно искать прямо в поле ввода: набираешь название — и сразу видишь книги с обложками."},
		Line{Text: "Для этого нужно один раз включить встроенный режим у твоего бота в @BotFather:"}, Gap{},
		Line{Text: "1. Открой @BotFather."},
		Code{Prefix: "2. Отправь ему ", Code: setInlineCommand, Suffix: "."},
		Line{Text: "3. Выбери " + bot + "."},
		Line{Text: "4. Отправь подсказку для поля ввода, например: Название или автор"}, Gap{},
		Line{Text: "После этого нажми «Проверить»."},
	}}, Keys: [][]Button{
		{{Text: ButtonOpenFather, URL: botFatherURL}},
		{{Text: ButtonCopyInline, CopyText: setInlineCommand}},
		{{Text: ButtonCheckInline, Env: k.Check}},
		{{Text: ButtonToSettings, Env: k.Back}},
	}}
}

// DonateScreen is S-DONATE: each method with its value written out, so a
// client without copy buttons can still copy it.
func DonateScreen(methods []config.DonateMethod, back *cbv2.Envelope) Screen {
	blocks := []Block{Title{Text: "Поддержать проект"}, Gap{},
		Line{Text: "Если библиотека тебе полезна, можно помочь её развитию."}}
	var keys [][]Button
	for i := range methods {
		m := &methods[i]
		switch m.Kind {
		case donateLink:
			keys = append(keys, []Button{{Text: m.Label, URL: m.Value}})
		default:
			blocks = append(blocks, Gap{}, Line{Text: m.Label}, Code{Code: m.Value})
			keys = append(keys, []Button{{Text: "Скопировать: " + ru.Truncate(m.Label, bookButtonRunes), CopyText: m.Value}})
			if m.Link != "" {
				keys = append(keys, []Button{{Text: m.Label, URL: m.Link}})
			}
		}
	}
	return Screen{Doc: Doc{Blocks: blocks}, Keys: append(keys, []Button{{Text: ButtonToSettings, Env: back}})}
}

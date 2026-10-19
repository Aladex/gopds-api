package screens

import (
	"gopds-api/services/kindlesend"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/ru"
)

// Send to Kindle from the card (plan.md P1-8): a confirmation, then the
// send's progress and outcome, in the same panel. The words follow the
// site's, in the bot's «ты».

// The Kindle words.
const (
	ButtonKindle      = "Kindle"
	ButtonKindleSend  = "Отправить"
	ButtonKindleSetup = "Настроить Kindle на сайте"
	kindleNotSet      = "не настроен"
)

// KindleSending is the status shown while the book is on its way, and
// KindleUnavailable a send stopped because the book is no longer handed out;
// neither is one of kindlesend's codes.
const (
	KindleSending     = "sending"
	KindleUnavailable = "unavailable"
)

// kindleCopy is each outcome in words; "" is sent.
var kindleCopy = map[string]string{
	"":                              "Отправлено на Kindle. Книга появится на устройстве через несколько минут.",
	KindleSending:                   "Отправляю на Kindle…",
	KindleUnavailable:               CopyUnavailable,
	kindlesend.CodeNotConfigured:    "Сначала укажи адрес Kindle на сайте.",
	kindlesend.CodeNotFound:         "Файл книги не найден.",
	kindlesend.CodeTooLarge:         "Книга слишком большая для Kindle — скачай EPUB.",
	kindlesend.CodeConversionFailed: "Не удалось подготовить EPUB для Kindle.",
	kindlesend.CodeSendFailed:       "Не удалось отправить на Kindle. Попробуй позже.",
	kindlesend.CodeInProgress:       "Эта книга уже отправляется на Kindle.",
	kindlesend.CodeBusy:             "Сейчас слишком много отправок на Kindle. Попробуй через минуту.",
}

// copySentHint follows a sent book.
const copySentHint = "Не пришла? Проверь, что адрес отправителя из профиля на сайте есть в списке одобренных на Amazon."

// KindleText is an outcome in words.
func KindleText(code string) string {
	if text, ok := kindleCopy[code]; ok {
		return text
	}
	return kindleCopy[kindlesend.CodeSendFailed]
}

// kindleName is the settings' Kindle line.
func kindleName(address string) string {
	if address == "" {
		return kindleNotSet
	}
	return address
}

// KindleConfirmScreen asks before mailing, as the site does.
func KindleConfirmScreen(title, address string, send, cancel *cbv2.Envelope) Screen {
	return Screen{Doc: Doc{Blocks: []Block{
		Line{Text: "Отправить «" + ru.Truncate(title, listLineRunes) + "» на Kindle " + address + "?"},
	}}, Keys: [][]Button{{{Text: ButtonKindleSend, Env: send}, {Text: ButtonCancel, Env: cancel}}}}
}

// KindleStatusScreen is a send's progress or outcome. setupURL, when the
// address is missing, leads to the site's Kindle setup.
func KindleStatusScreen(title, code, setupURL string, back, home *cbv2.Envelope) Screen {
	blocks := []Block{Title{Text: ru.Truncate(title, listLineRunes)}, Gap{}, Line{Text: KindleText(code)}}
	if code == "" {
		blocks = append(blocks, Line{Text: copySentHint})
	}
	var keys [][]Button
	if code == kindlesend.CodeNotConfigured && setupURL != "" {
		keys = append(keys, []Button{{Text: ButtonKindleSetup, URL: setupURL}})
	}
	return Screen{Doc: Doc{Blocks: blocks}, Keys: append(keys, []Button{{Text: ButtonToBook, Env: back}, {Text: ButtonHome, Env: home}})}
}

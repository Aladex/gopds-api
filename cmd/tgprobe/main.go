// Command tgprobe asks the real Bot API the questions the bot's interface
// depends on and the documentation leaves open (plan-ui.md §5.1, Gate 0):
// whether a message may be edited between plain text and rich content, how a
// cover photo uploads into a rich message and comes back as a file_id, how
// pins and the command menu behave.
//
// It is run by hand against a throwaway test bot:
//
//	go run ./cmd/tgprobe -secrets /path/to/file [-cover-url https://proxy/books-posters/x.jpg]
//
// With -preview lists it asks nothing: it sends the list screens as the bot
// lays them out in rich messages, from fixture data, and answers their
// buttons for -wait (10 minutes by default) or until Ctrl+C:
//
//	go run ./cmd/tgprobe -secrets /path/to/file -preview lists [-wait 30m]
//
// A bot with a webhook — one that serves something else — gets -send-only:
// the lists, then each again as a press would leave it (the results' next
// page and authors tab, the author's next page), as messages of their own.
// It reads no updates and does not touch the webhook; the buttons go
// unanswered:
//
//	go run ./cmd/tgprobe -secrets /path/to/file -preview lists -send-only
//
// The file holds two lines, TG_TEST_TOKEN=… and TG_TEST_CHAT=… (the private
// chat of the person looking at the result). The token is never printed:
// every error passes through tgapi.RedactURL before it is shown. The probe
// changes the test bot's commands and menu button, and pins one message.
package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"gopds-api/telegram/tgapi"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Shapes of the probe's messages and its exit code on bad usage.
const (
	exitUsage    = 2
	coverWidth   = 120
	coverHeight  = 180
	tooManyCells = 21 // one over the documented 20 columns
	alignLeft    = "left"
	alignTop     = "top"
	coverAttach  = "attach://cover"
	headingSize  = 2
	coverBlue    = 120
	opaque       = 255
	probeTimeout = 2 * time.Minute
)

// result is one answered question.
type result struct {
	question string
	answer   string
}

func main() {
	secretsPath := flag.String("secrets", "", "file with TG_TEST_TOKEN=… and TG_TEST_CHAT=… lines")
	coverURL := flag.String("cover-url", "", "optional: a cover URL on the proxy host, fetched from here")
	preview := flag.String("preview", "", `"lists": send the list screens as rich messages and answer their buttons`)
	wait := flag.Duration("wait", previewWait, "with -preview: how long the buttons keep working")
	sendOnly := flag.Bool("send-only", false,
		"with -preview: send the lists and their next states, read no updates, leave the webhook alone")
	flag.Parse()
	token, chat, err := readSecrets(*secretsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tgprobe:", err)
		os.Exit(exitUsage)
	}
	switch *preview {
	case "":
	case "lists":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		ctx, cancel := context.WithTimeout(ctx, *wait)
		previewErr := runPreview(ctx, token, chat, *sendOnly)
		cancel()
		stop()
		if previewErr != nil {
			fmt.Fprintln(os.Stderr, "tgprobe:", previewErr)
			os.Exit(1)
		}
		return
	default:
		fmt.Fprintln(os.Stderr, "tgprobe: -preview knows only \"lists\"")
		os.Exit(exitUsage)
	}
	client, err := tgapi.New(token, tgapi.Endpoint{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "tgprobe: building the client:", tgapi.RedactURL(err))
		os.Exit(exitUsage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	p := &probe{ctx: ctx, b: client, chat: chat}
	p.run()
	if *coverURL != "" {
		p.add("proxy host serves the cover URL", fetch(ctx, *coverURL))
	}
	for _, r := range p.results {
		fmt.Printf("| %s | %s |\n", r.question, r.answer)
	}
}

// readSecrets reads the token and the chat id from path.
func readSecrets(path string) (token string, chat int64, err error) {
	if path == "" {
		return "", 0, errors.New("-secrets is required")
	}
	f, err := os.Open(path) // #nosec G304 -- the operator names the file
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, value, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "TG_TEST_TOKEN":
			token = strings.TrimSpace(value)
		case "TG_TEST_CHAT":
			chat, err = strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil {
				return "", 0, errors.New("TG_TEST_CHAT is not a number")
			}
		}
	}
	if token == "" || chat == 0 {
		return "", 0, errors.New("the file needs both TG_TEST_TOKEN and TG_TEST_CHAT")
	}
	return token, chat, sc.Err()
}

type probe struct {
	ctx     context.Context
	b       *bot.Bot
	chat    int64
	results []result
}

func (p *probe) add(question, answer string) {
	p.results = append(p.results, result{question, answer})
}

// verdict is "ok", or the redacted error.
func verdict(err error) string {
	if err == nil {
		return "ok"
	}
	return "refused: " + tgapi.RedactURL(err).Error()
}

func plain(s string) models.RichText { return models.RichText{PlainText: s} }

func paragraph(s string) models.InputRichBlock {
	return models.InputRichBlock{Type: models.RichBlockTypeParagraph,
		InputRichBlockParagraph: &models.InputRichBlockParagraph{Text: plain(s)}}
}

func keyboard(label string) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{{Text: label, CallbackData: "probe"}}}}
}

func (p *probe) run() {
	p.textToRich()
	p.richToText()
	fileID := p.richWithUpload()
	p.richEditWithFileID(fileID)
	p.richEditWithUpload()
	p.rejection()
	p.disabledButton()
	p.pin()
	p.commands()
}

// textToRich: may a text message become rich by editMessageText?
func (p *probe) textToRich() {
	msg, err := p.b.SendMessage(p.ctx, &bot.SendMessageParams{ChatID: p.chat, Text: "tgprobe: text, about to become rich"})
	if err != nil {
		p.add("send a text message", verdict(err))
		return
	}
	_, err = p.b.EditMessageText(p.ctx, &bot.EditMessageTextParams{ChatID: p.chat, MessageID: msg.ID,
		RichMessage: &models.InputRichMessage{Blocks: []models.InputRichBlock{paragraph("tgprobe: now rich")}}})
	p.add("edit text → rich (editMessageText rich_message)", verdict(err))
}

// richToText: may a rich message become plain text?
func (p *probe) richToText() {
	msg, err := p.b.SendRichMessage(p.ctx, &bot.SendRichMessageParams{ChatID: p.chat,
		RichMessage: models.InputRichMessage{Blocks: []models.InputRichBlock{paragraph("tgprobe: rich, about to become text")}}})
	if err != nil {
		p.add("send a rich message", verdict(err))
		return
	}
	_, err = p.b.EditMessageText(p.ctx, &bot.EditMessageTextParams{ChatID: p.chat, MessageID: msg.ID,
		Text: "tgprobe: now plain"})
	p.add("edit rich → text (editMessageText text)", verdict(err))
}

// cover is a small generated JPEG.
func cover() []byte {
	img := image.NewRGBA(image.Rect(0, 0, coverWidth, coverHeight))
	for y := 0; y < coverHeight; y++ {
		for x := 0; x < coverWidth; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: coverBlue, A: opaque}) // #nosec G115 -- bounded by the loop
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, nil)
	return buf.Bytes()
}

// card is a rich card shaped like the bot's: photo, heading, table, details.
func card(photo *models.InputMediaPhoto, title string) models.InputRichMessage {
	cell := func(s string, header bool) models.RichBlockTableCell {
		t := plain(s)
		return models.RichBlockTableCell{Text: &t, IsHeader: header, Align: alignLeft, Valign: alignTop}
	}
	return models.InputRichMessage{Blocks: []models.InputRichBlock{
		{Type: models.RichBlockTypePhoto, InputRichBlockPhoto: &models.InputRichBlockPhoto{Photo: *photo}},
		{Type: models.RichBlockTypeSectionHeading,
			InputRichBlockSectionHeading: &models.InputRichBlockSectionHeading{Text: plain(title), Size: headingSize}},
		paragraph("Автор Пробный"),
		{Type: models.RichBlockTypeTable, InputRichBlockTable: &models.InputRichBlockTable{IsCompact: true,
			Cells: [][]models.RichBlockTableCell{
				{cell("Серия", true), cell("Пробная, кн. 1", false)},
				{cell("Язык", true), cell("русский", false)},
			}}},
		{Type: models.RichBlockTypeDetails, InputRichBlockDetails: &models.InputRichBlockDetails{Summary: plain("Аннотация"),
			Blocks: []models.InputRichBlock{paragraph("Текст аннотации.")}}},
	}}
}

// richWithUpload sends a card with an uploaded cover and returns its file_id.
func (p *probe) richWithUpload() string {
	photo := &models.InputMediaPhoto{Media: coverAttach, MediaAttachment: bytes.NewReader(cover())}
	msg, err := p.b.SendRichMessage(p.ctx, &bot.SendRichMessageParams{ChatID: p.chat,
		RichMessage: card(photo, "tgprobe: upload"), ReplyMarkup: keyboard("EPUB")})
	p.add("sendRichMessage with photo upload, table, details, keyboard", verdict(err))
	if err != nil {
		return ""
	}
	fileID := photoFileID(msg)
	if fileID == "" {
		p.add("photo file_id in the returned rich_message", "absent")
		return ""
	}
	p.add("photo file_id in the returned rich_message", "present")
	if msg.ReplyMarkup != nil {
		p.add("keyboard on the returned rich message", "present")
	} else {
		p.add("keyboard on the returned rich message", "absent")
	}
	return fileID
}

// photoFileID is the largest photo size's file_id in a rich message.
func photoFileID(msg *models.Message) string {
	if msg == nil || msg.RichMessage == nil {
		return ""
	}
	for i := range msg.RichMessage.Blocks {
		if photo := msg.RichMessage.Blocks[i].RichBlockPhoto; photo != nil && len(photo.Photo) > 0 {
			return photo.Photo[len(photo.Photo)-1].FileID
		}
	}
	return ""
}

// richEditWithFileID edits a rich message into a card that reuses fileID,
// without a keyboard in the call: does the keyboard survive?
func (p *probe) richEditWithFileID(fileID string) {
	if fileID == "" {
		p.add("edit rich → rich reusing the file_id", "skipped (no file_id)")
		return
	}
	msg, err := p.b.SendRichMessage(p.ctx, &bot.SendRichMessageParams{ChatID: p.chat,
		RichMessage: models.InputRichMessage{Blocks: []models.InputRichBlock{paragraph("tgprobe: before the file_id edit")}},
		ReplyMarkup: keyboard("FB2")})
	if err != nil {
		p.add("edit rich → rich reusing the file_id", verdict(err))
		return
	}
	rich := card(&models.InputMediaPhoto{Media: fileID}, "tgprobe: file_id")
	edited, err := p.b.EditMessageText(p.ctx, &bot.EditMessageTextParams{ChatID: p.chat, MessageID: msg.ID, RichMessage: &rich})
	p.add("edit rich → rich reusing the file_id", verdict(err))
	if err == nil {
		kept := "dropped"
		if edited != nil && edited.ReplyMarkup != nil {
			kept = "kept"
		}
		p.add("keyboard after an edit that names none", kept)
	}
}

// richEditWithUpload edits a rich message with a new upload.
func (p *probe) richEditWithUpload() {
	msg, err := p.b.SendRichMessage(p.ctx, &bot.SendRichMessageParams{ChatID: p.chat,
		RichMessage: models.InputRichMessage{Blocks: []models.InputRichBlock{paragraph("tgprobe: before the upload edit")}}})
	if err != nil {
		p.add("edit rich → rich with a new upload", verdict(err))
		return
	}
	rich := card(&models.InputMediaPhoto{Media: coverAttach, MediaAttachment: bytes.NewReader(cover())}, "tgprobe: edit upload")
	_, err = p.b.EditMessageText(p.ctx, &bot.EditMessageTextParams{ChatID: p.chat, MessageID: msg.ID, RichMessage: &rich,
		ReplyMarkup: keyboard("MOBI")})
	p.add("edit rich → rich with a new upload", verdict(err))
}

// rejection records the error a rich message that breaks a limit draws: 21
// table columns, one over the documented 20.
func (p *probe) rejection() {
	row := make([]models.RichBlockTableCell, tooManyCells)
	for i := range row {
		t := plain(strconv.Itoa(i))
		row[i] = models.RichBlockTableCell{Text: &t, Align: alignLeft, Valign: alignTop}
	}
	_, err := p.b.SendRichMessage(p.ctx, &bot.SendRichMessageParams{ChatID: p.chat, RichMessage: models.InputRichMessage{
		Blocks: []models.InputRichBlock{{Type: models.RichBlockTypeTable, InputRichBlockTable: &models.InputRichBlockTable{
			Cells: [][]models.RichBlockTableCell{row}}}}}})
	p.add("rich message with 21 table columns (error text)", verdict(err))
}

// disabledButton sends a disabled inline button.
func (p *probe) disabledButton() {
	_, err := p.b.SendMessage(p.ctx, &bot.SendMessageParams{ChatID: p.chat, Text: "tgprobe: a disabled button",
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
			{Text: "⏳ Готовлю EPUB…", Disabled: &models.DisabledButton{}}}}}})
	p.add("disabled inline button accepted", verdict(err))
}

// pin pins a message and reads it back through getChat.
func (p *probe) pin() {
	msg, err := p.b.SendMessage(p.ctx, &bot.SendMessageParams{ChatID: p.chat, Text: "tgprobe: pinned"})
	if err != nil {
		p.add("pinChatMessage", verdict(err))
		return
	}
	_, err = p.b.PinChatMessage(p.ctx, &bot.PinChatMessageParams{ChatID: p.chat, MessageID: msg.ID, DisableNotification: true})
	p.add("pinChatMessage", verdict(err))
	chat, err := p.b.GetChat(p.ctx, &bot.GetChatParams{ChatID: p.chat})
	switch {
	case err != nil:
		p.add("getChat.pinned_message", verdict(err))
	case chat.PinnedMessage != nil && chat.PinnedMessage.ID == msg.ID:
		p.add("getChat.pinned_message", "the pinned message")
	default:
		p.add("getChat.pinned_message", "another or none")
	}
	p.add("service line «… pinned …» in the chat", "look at the chat")
}

// commands clears the command menu the way the bot will.
func (p *probe) commands() {
	var failed []string
	scopes := []models.BotCommandScope{&models.BotCommandScopeDefault{}, &models.BotCommandScopeAllPrivateChats{},
		&models.BotCommandScopeChat{ChatID: p.chat}}
	for _, scope := range scopes {
		for _, lang := range []string{"", "ru", "en"} {
			if _, err := p.b.DeleteMyCommands(p.ctx, &bot.DeleteMyCommandsParams{Scope: scope, LanguageCode: lang}); err != nil {
				failed = append(failed, verdict(err))
			}
		}
	}
	if len(failed) == 0 {
		p.add("deleteMyCommands × 9", "ok")
	} else {
		p.add("deleteMyCommands × 9", strings.Join(failed, "; "))
	}
	menu := &models.MenuButtonDefault{Type: models.MenuButtonTypeDefault}
	_, err := p.b.SetChatMenuButton(p.ctx, &bot.SetChatMenuButtonParams{MenuButton: menu})
	p.add("setChatMenuButton default (bot)", verdict(err))
	_, err = p.b.SetChatMenuButton(p.ctx, &bot.SetChatMenuButtonParams{ChatID: p.chat, MenuButton: menu})
	p.add("setChatMenuButton default (chat)", verdict(err))
	p.add("menu button / command suggestions on «/»", "look at the chat")
}

// fetch reports how url answers from here.
func fetch(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err.Error()
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	return fmt.Sprintf("%d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
}

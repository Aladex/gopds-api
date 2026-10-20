package router

import (
	"context"
	"strings"

	"gopds-api/database"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/telegram/cbv2"
	"gopds-api/telegram/screens"
	"gopds-api/telegram/tgapi"
)

// The settings (plan-ui.md §1.9): what OpSettings' Arg opens. The search
// language is the language screen with no view (languageNode).
const (
	sectionSettings uint8 = iota
	sectionFormat
	_ // the search language: OpLanguagePicker
	sectionCardStyle
	sectionInline
	sectionDonate
	sectionRecent
	sectionClearRecent
)

// sectionScreens are the settings' screens by section.
var sectionScreens = map[uint8]Screen{
	sectionSettings:  ScrSettings,
	sectionFormat:    ScrFormat,
	sectionCardStyle: ScrCardStyle,
	sectionInline:    ScrInline,
	sectionDonate:    ScrDonate,
	// The recent sends' settings, and the clearing's confirmation.
	sectionRecent:      ScrRecentSettings,
	sectionClearRecent: ScrClearRecent,
}

// openSettings moves a panel to a settings screen.
func (r *Router) openSettings(ctx context.Context, b *Bot, panel *models.TelegramPanel, env *cbv2.Envelope) string {
	screen, ok := sectionScreens[env.Arg]
	if !ok || (screen == ScrDonate && len(r.cfg.Donate) == 0) {
		return screens.CopyUnsupported
	}
	parent := int64(env.ReturnID) // #nosec G115 -- decoded ids fit
	node := &models.TelegramNode{UserID: b.Binding.UserID, Screen: int16(screen), ParentID: parent}
	return r.show(ctx, b, panel, panel.Revision, node)
}

// settingsPainter reads a settings screen.
func (r *Router) settingsPainter(ctx context.Context, b *Bot, node *models.TelegramNode) (render, string, bool) {
	ui, err := database.GetTelegramUserUI(ctx, b.Binding.UserID)
	if err != nil {
		return nil, screens.CopyLibraryDown, false
	}
	plain := ui.CardStyle == models.TelegramPanelPlain
	inline := r.inlineOn(ctx, b)
	kindle, err := kindleAddress(b)
	if err != nil {
		return nil, screens.CopyLibraryDown, false
	}
	return func(k *keys) screens.Screen {
		parent, self := uint64(node.ParentID), uint64(node.ID) // #nosec G115 -- serial ids
		back := k.env(cbv2.Envelope{Op: cbv2.OpBack, ReturnID: parent})
		section := func(arg uint8) *cbv2.Envelope {
			return k.env(cbv2.Envelope{Op: cbv2.OpSettings, Arg: arg, ReturnID: self})
		}
		//exhaustive:ignore // the settings' sub-screens; the rest is the settings
		switch Screen(node.Screen) {
		case ScrFormat:
			return screens.FormatScreen(ui.DefaultFormat, func(f string) *cbv2.Envelope {
				return k.env(cbv2.Envelope{Op: cbv2.OpSetFormat, Arg: screens.FormatArg(f), ReturnID: parent})
			}, back)
		case ScrCardStyle:
			return screens.CardStyleScreen(plain, func(p bool) *cbv2.Envelope {
				arg := uint8(0)
				if p {
					arg = 1
				}
				return k.env(cbv2.Envelope{Op: cbv2.OpSetCardStyle, Arg: arg, ReturnID: parent})
			}, back)
		case ScrInline:
			state := screens.InlineOff
			switch {
			case inline && node.Page == inlineJustOn:
				state = screens.InlineJustOn
			case inline:
				state = screens.InlineOn
			}
			return screens.InlineScreen(b.Username, state, &screens.InlineKeys{
				Check: k.env(cbv2.Envelope{Op: cbv2.OpInlineCheck, ReturnID: parent}), Back: back, Home: k.home(),
			})
		case ScrDonate:
			return screens.DonateScreen(r.cfg.Donate, back)
		case ScrRecentSettings:
			return screens.RecentSettingsScreen(ui.KeepRecent, &screens.RecentSettingsKeys{
				Keep: func(keep bool) *cbv2.Envelope {
					arg := uint8(0)
					if keep {
						arg = 1
					}
					return k.env(cbv2.Envelope{Op: cbv2.OpSetKeepRecent, Arg: arg, ReturnID: parent})
				},
				Clear: section(sectionClearRecent),
				Back:  back,
			})
		case ScrClearRecent:
			return screens.ClearRecentScreen(k.env(cbv2.Envelope{Op: cbv2.OpClearRecent, ReturnID: parent}), back)
		}
		sk := &screens.SettingsKeys{
			Format:   section(sectionFormat),
			Language: k.env(cbv2.Envelope{Op: cbv2.OpLanguagePicker, ReturnID: self}),
			Style:    section(sectionCardStyle),
			Inline:   section(sectionInline),
			Recent:   section(sectionRecent),
			Home:     k.home(),
			// The address is set on the site, where it is checked.
			KindleURL: r.kindleSetupURL(),
		}
		if len(r.cfg.Donate) > 0 {
			sk.Donate = section(sectionDonate)
		}
		return screens.SettingsScreen(ui.DefaultFormat, ui.SearchLang, plain, inline, ui.KeepRecent, kindle, sk)
	}, "", true
}

// setFormat saves the default format and paints its screen again.
func (r *Router) setFormat(ctx context.Context, b *Bot, panel *models.TelegramPanel, env *cbv2.Envelope) string {
	format := screens.FormatOf(env.Arg)
	if format == "" {
		return screens.CopyUnsupported
	}
	if err := database.SetTelegramDefaultFormat(ctx, b.Binding.UserID, format); err != nil {
		return failedWrite(b, "saving the default format", err)
	}
	r.repaint(ctx, b, panel)
	return "Основной формат — " + strings.ToUpper(format)
}

// setCardStyle saves the card style and paints its screen again. Panels
// already sent keep their kind of message; the next one sent follows it, and
// a rich panel goes plain on its next change (movePanel).
func (r *Router) setCardStyle(ctx context.Context, b *Bot, panel *models.TelegramPanel, env *cbv2.Envelope) string {
	style := models.TelegramPanelRich
	if env.Arg == 1 {
		style = models.TelegramPanelPlain
	}
	if err := database.SetTelegramCardStyle(ctx, b.Binding.UserID, style); err != nil {
		return failedWrite(b, "saving the card style", err)
	}
	r.repaint(ctx, b, panel)
	return "Вид карточек — " + screens.StyleName(style == models.TelegramPanelPlain)
}

// repaint shows a panel's node again, at a new revision.
func (r *Router) repaint(ctx context.Context, b *Bot, panel *models.TelegramPanel) {
	node, err := database.GetTelegramNode(ctx, b.Binding.UserID, panel.NodeID)
	if err != nil {
		return
	}
	r.show(ctx, b, panel, panel.Revision, node)
}

// inlineJustOn is the page of S-INLINE that «Проверить» found inline mode on.
const inlineJustOn = 1

// checkInline is «Проверить»: getMe, then the screen that says it is on —
// and the home panel offers inline search at once — or a toast that it is
// not yet.
func (r *Router) checkInline(ctx context.Context, b *Bot, panel *models.TelegramPanel, env *cbv2.Envelope) string {
	on, err := r.bots.CheckInline(ctx, b.Ref)
	if err != nil {
		logging.Warnf("Telegram bot %d: checking inline mode: %v", b.Ref, tgapi.RedactURL(err))
		return screens.CopyLibraryDown
	}
	if !on {
		return screens.CopyInlineNotYet
	}
	node := &models.TelegramNode{UserID: b.Binding.UserID, Screen: int16(ScrInline), Page: inlineJustOn,
		ParentID: int64(env.ReturnID)} // #nosec G115 -- decoded ids fit
	toast := r.show(ctx, b, panel, panel.Revision, node)
	if !panel.IsHome {
		r.repaintHome(ctx, b, panel.ChatID)
	}
	return toast
}

// repaintHome paints the chat's home panel again, if it shows home.
func (r *Router) repaintHome(ctx context.Context, b *Bot, chatID int64) {
	home, err := database.GetTelegramHomePanel(ctx, b.Ref, chatID)
	if err != nil || home == nil {
		return
	}
	node, err := database.GetTelegramNode(ctx, b.Binding.UserID, home.NodeID)
	if err != nil || Screen(node.Screen) != ScrHome {
		return
	}
	r.show(ctx, b, home, home.Revision, node)
}

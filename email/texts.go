package email

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// The wording of every email, in both languages the interface offers.
//
// It lives here rather than in the markup because it used to live in both: the
// title, message, button and signature came from the operator's configuration
// in whatever language they had chosen, while the line introducing the link,
// the caution and the footer were written into the template in English. A
// Russian email therefore explained itself in English halfway through.
//
// Everything below is a default. Any of it can be overridden per language and
// per email in the configuration, and what is left alone still arrives in the
// language the operator asked for — which is the other half of the problem:
// nothing was configured by default, so an instance that had not filled these
// in sent emails with an empty subject, an empty heading and a nameless button.

// DefaultLanguage is what an installation that names none gets. English is the
// safer of the two to be wrong about.
const DefaultLanguage = LanguageEN

// The two languages the interface offers, and therefore the two it writes in.
const (
	LanguageEN = "en"
	LanguageRU = "ru"
)

// DefaultProductName names an installation that has not named itself.
const DefaultProductName = "Booksdump"

// The two kinds of email this application sends.
const (
	kindRegistration = "registration"
	kindReset        = "reset"
)

// The one line both emails share, per language.
const (
	linkFallbackRU = "Если кнопка не работает, скопируйте эту ссылку в браузер:"
	linkFallbackEN = "If the button doesn't work, copy this link into your browser:"
)

// texts is one email's wording.
type texts struct {
	Subject      string
	Title        string
	Message      string
	Button       string
	Thanks       string
	LinkFallback string
	Warning      string
	Footer       string
}

// builtin returns the wording this application ships with.
//
// None of it names the product except the signature and the footer, where not
// naming it would be strange: the wordmark at the head of the email has already
// said it once, and repeating it in the heading — "Password reset at
// BOOKSDUMP", under a bar reading Booksdump — only says it twice.
func builtin(lang, kind, product string) texts {
	if lang == LanguageRU {
		switch kind {
		case kindRegistration:
			const heading = "Активация аккаунта"
			return texts{
				Subject:      heading,
				Title:        heading,
				Message:      "Нажмите кнопку, чтобы подтвердить адрес и войти.",
				Button:       "Активировать",
				Thanks:       fmt.Sprintf("С уважением, команда %s", product),
				LinkFallback: linkFallbackRU,
				Footer:       fmt.Sprintf("Вы получили это письмо, потому что зарегистрировались на %s.", product),
			}
		case kindReset:
			const heading = "Сброс пароля"
			return texts{
				Subject:      heading,
				Title:        heading,
				Message:      "Нажмите кнопку, чтобы задать новый пароль. Ссылка действует 90 минут.",
				Button:       "Задать новый пароль",
				Thanks:       fmt.Sprintf("С уважением, команда %s", product),
				LinkFallback: linkFallbackRU,
				Warning:      "Если вы не запрашивали смену пароля, просто не открывайте ссылку — пароль останется прежним.",
				Footer:       fmt.Sprintf("Вы получили это письмо, потому что для вашего аккаунта на %s запросили смену пароля.", product),
			}
		}
	}

	switch kind {
	case kindRegistration:
		const heading = "Activate your account"
		return texts{
			Subject:      heading,
			Title:        heading,
			Message:      "Press the button to confirm your address and sign in.",
			Button:       "Activate",
			Thanks:       fmt.Sprintf("Best regards, the %s team", product),
			LinkFallback: linkFallbackEN,
			Footer:       fmt.Sprintf("You received this email because you registered on %s.", product),
		}
	case kindReset:
		const heading = "Reset your password"
		return texts{
			Subject:      heading,
			Title:        heading,
			Message:      "Press the button to choose a new password. The link is good for 90 minutes.",
			Button:       "Choose a new password",
			Thanks:       fmt.Sprintf("Best regards, the %s team", product),
			LinkFallback: linkFallbackEN,
			Warning:      "If you didn't ask to change your password, simply don't open the link — your password stays as it is.",
			Footer:       fmt.Sprintf("You received this email because a password reset was requested for your %s account.", product),
		}
	}

	return texts{}
}

// language is the one the operator asked for, or the default.
func language() string {
	lang := strings.ToLower(strings.TrimSpace(viper.GetString("email.language")))
	if lang != LanguageRU && lang != LanguageEN {
		return DefaultLanguage
	}
	return lang
}

// productName is what this installation calls itself.
func productName() string {
	if name := strings.TrimSpace(viper.GetString("email.product_name")); name != "" {
		return name
	}
	return DefaultProductName
}

// resolve is the wording for one email: what this application ships with, with
// anything the operator has set in its place.
func resolve(kind string) texts {
	lang := language()
	out := builtin(lang, kind, productName())

	prefix := fmt.Sprintf("email.messages.%s.%s.", lang, kind)
	override := func(field *string, key string) {
		// An empty setting means "leave it alone" rather than "make it empty":
		// a subject nobody wrote is not a subject anybody wanted blank.
		if value := strings.TrimSpace(viper.GetString(prefix + key)); value != "" {
			*field = value
		}
	}

	override(&out.Subject, "subject")
	override(&out.Title, "title")
	override(&out.Message, "message")
	override(&out.Button, "button")
	override(&out.Thanks, "thanks")
	override(&out.LinkFallback, "link_fallback")
	override(&out.Warning, "warning")
	override(&out.Footer, "footer")

	return out
}

// apply fills one email's wording into the message being sent, leaving the
// address and the link the caller supplied.
func (t *texts) apply(data *SendType) {
	data.Subject = t.Subject
	data.Title = t.Title
	data.Message = t.Message
	data.Button = t.Button
	data.Thanks = t.Thanks
	data.LinkFallback = t.LinkFallback
	data.Warning = t.Warning
	data.Footer = t.Footer
}

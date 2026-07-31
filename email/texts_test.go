package email

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// The wording used to come from two places at once: the configuration for the
// title, message, button and signature, and the markup for the three lines
// around them, in English. These pin the arrangement that replaced it — one
// language, chosen in one place, covering the whole email.

func setConfig(t *testing.T, values map[string]string) {
	t.Helper()

	for key, value := range values {
		viper.Set(key, value)
	}
	t.Cleanup(func() {
		for key := range values {
			viper.Set(key, "")
		}
	})
}

func TestTheChosenLanguageCoversTheWholeEmail(t *testing.T) {
	setConfig(t, map[string]string{"email.language": "ru", "email.product_name": "Booksdump"})

	got := resolve(kindReset)

	// Every field, not just the ones that used to come from configuration.
	fields := map[string]string{
		"subject":       got.Subject,
		"title":         got.Title,
		"message":       got.Message,
		"button":        got.Button,
		"thanks":        got.Thanks,
		"link fallback": got.LinkFallback,
		"warning":       got.Warning,
		"footer":        got.Footer,
	}
	for name, value := range fields {
		if value == "" {
			t.Errorf("the %s is empty", name)
			continue
		}
		if !strings.ContainsAny(value, "абвгдеёжзийклмнопрстуфхцчшщъыьэюя") {
			t.Errorf("the %s is not in the chosen language: %q", name, value)
		}
	}
}

func TestEnglishIsWhatAnUnknownLanguageGets(t *testing.T) {
	for _, lang := range []string{"", "fr", "  ", "klingon"} {
		t.Run(lang, func(t *testing.T) {
			setConfig(t, map[string]string{"email.language": lang})

			if got := resolve(kindReset); !strings.Contains(got.Subject, "Reset") {
				t.Errorf("subject is %q, expected the English wording", got.Subject)
			}
		})
	}
}

func TestLanguageIsReadWithoutCeremony(t *testing.T) {
	for _, lang := range []string{"ru", "RU", " ru ", "Ru"} {
		t.Run(lang, func(t *testing.T) {
			setConfig(t, map[string]string{"email.language": lang})

			if got := language(); got != "ru" {
				t.Errorf("%q was read as %q", lang, got)
			}
		})
	}
}

// Nothing configured must still be a complete email. It was not: no defaults
// existed, so an instance that had filled none of this in sent an empty
// subject, an empty heading and a nameless button.
func TestAnUnconfiguredInstanceStillSendsACompleteEmail(t *testing.T) {
	for _, kind := range []string{kindRegistration, kindReset} {
		t.Run(kind, func(t *testing.T) {
			got := resolve(kind)

			if got.Subject == "" || got.Title == "" || got.Message == "" {
				t.Error("the email has nothing to say")
			}
			if got.Button == "" {
				t.Error("the button has no label")
			}
			if got.Thanks == "" || got.LinkFallback == "" || got.Footer == "" {
				t.Error("the wording around the message is missing")
			}
		})
	}
}

func TestOnlyAResetCarriesAWarning(t *testing.T) {
	if resolve(kindRegistration).Warning != "" {
		t.Error("a registration warns about something")
	}
	if resolve(kindReset).Warning == "" {
		t.Error("a password reset warns about nothing")
	}
}

// The configuration overrides one field at a time: setting a signature must not
// blank out the subject beside it.
func TestAnOverrideLeavesTheRestAlone(t *testing.T) {
	setConfig(t, map[string]string{
		"email.language":                        "en",
		"email.messages.en.reset.thanks":        "Yours, the library",
		"email.messages.en.reset.link_fallback": "Or paste this:",
	})

	got := resolve(kindReset)

	if got.Thanks != "Yours, the library" {
		t.Errorf("the signature was not taken from the configuration: %q", got.Thanks)
	}
	if got.LinkFallback != "Or paste this:" {
		t.Errorf("the fallback line was not taken from the configuration: %q", got.LinkFallback)
	}
	if got.Subject == "" || !strings.Contains(got.Subject, "Reset") {
		t.Errorf("an untouched field was lost: %q", got.Subject)
	}
}

// An empty setting means "say nothing about it", not "make it blank".
func TestAnEmptySettingIsNotAnOverride(t *testing.T) {
	setConfig(t, map[string]string{
		"email.language":                  "en",
		"email.messages.en.reset.subject": "   ",
	})

	if got := resolve(kindReset); got.Subject == "" || strings.TrimSpace(got.Subject) == "" {
		t.Errorf("a blank setting emptied the subject: %q", got.Subject)
	}
}

// Overrides are read from the language actually being sent.
func TestAnOverrideBelongsToItsOwnLanguage(t *testing.T) {
	setConfig(t, map[string]string{
		"email.language":                  "ru",
		"email.messages.en.reset.subject": "Only for English",
	})

	if got := resolve(kindReset); got.Subject == "Only for English" {
		t.Error("an English override reached a Russian email")
	}
}

// Anyone running their own copy should not send mail signed Booksdump.
func TestTheProductNamesItself(t *testing.T) {
	setConfig(t, map[string]string{"email.language": "en", "email.product_name": "Knizhki"})

	got := resolve(kindRegistration)

	if !strings.Contains(got.Thanks, "Knizhki") {
		t.Errorf("the signature does not name the installation: %q", got.Thanks)
	}
	if !strings.Contains(got.Footer, "Knizhki") {
		t.Errorf("the footer does not name the installation: %q", got.Footer)
	}
}

func TestAnUnnamedInstallationFallsBackToADefault(t *testing.T) {
	setConfig(t, map[string]string{"email.product_name": "  "})

	if got := productName(); got != DefaultProductName {
		t.Errorf("product name is %q, want %q", got, DefaultProductName)
	}
}

// The heading used to name the product under a bar that had just named it.
func TestTheHeadingDoesNotRepeatTheWordmark(t *testing.T) {
	setConfig(t, map[string]string{"email.language": "en", "email.product_name": "Booksdump"})

	for _, kind := range []string{kindRegistration, kindReset} {
		if title := resolve(kind).Title; strings.Contains(title, "Booksdump") {
			t.Errorf("the %s heading repeats the wordmark: %q", kind, title)
		}
	}
}

// The link expires in 90 minutes and no email used to say so.
func TestTheResetSaysHowLongTheLinkLasts(t *testing.T) {
	for _, lang := range []string{"en", "ru"} {
		t.Run(lang, func(t *testing.T) {
			setConfig(t, map[string]string{"email.language": lang})

			if got := resolve(kindReset); !strings.Contains(got.Message, "90") {
				t.Errorf("the message does not say when the link expires: %q", got.Message)
			}
		})
	}
}

package email

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// MailConnection reaches the network, so what is testable here is everything it
// does before that: reading the address and refusing to go on when it is not
// one. That refusal is the point — the version before it dropped the parse
// error and carried on with an empty host, which disabled the certificate check
// and then offered the mailbox password to whatever answered.

func TestMailConnectionRejectsAnAddressWithoutAPort(t *testing.T) {
	cases := []string{
		"",
		"smtp.example.test",
		"smtp.example.test:465:465",
	}

	for _, address := range cases {
		t.Run(address, func(t *testing.T) {
			viper.Set("email.smtp_server", address)
			t.Cleanup(func() { viper.Set("email.smtp_server", "") })

			client, err := MailConnection()
			if err == nil {
				t.Fatalf("accepted %q as a server address", address)
			}
			if client != nil {
				t.Error("returned a client alongside the error")
			}
			if !strings.Contains(err.Error(), "host:port") {
				t.Errorf("the error does not say what was expected of the value: %v", err)
			}
			if !strings.Contains(err.Error(), address) && address != "" {
				t.Errorf("the error does not quote the offending value: %v", err)
			}
		})
	}
}

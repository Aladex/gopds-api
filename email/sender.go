package email

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"html/template"
	"net"
	"net/mail"
	"net/smtp"

	assets "gopds-api"
	"gopds-api/logging"

	"github.com/spf13/viper"
)

type SendType struct {
	Title, Token, Button, Message, Email, Subject, Thanks string
}

func MailConnection() (*smtp.Client, error) {
	servername := viper.GetString("email.smtp_server")

	// The error was dropped here, which turned a misconfigured address into an
	// empty host: the certificate would then be checked against nothing, and
	// the SMTP AUTH would offer the password to whoever answered.
	host, _, err := net.SplitHostPort(servername)
	if err != nil {
		return nil, fmt.Errorf("email.smtp_server must be host:port, got %q: %w", servername, err)
	}

	// Verification used to be off. Nothing needed it: the certificate this
	// server presents names the host we dial. With it off, anything able to
	// answer on that address was handed the mailbox password on the next line.
	tlsconfig := &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	}
	conn, err := tls.Dial("tcp", servername, tlsconfig)
	if err != nil {
		return nil, err
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return nil, err
	}

	auth := smtp.PlainAuth("", viper.GetString("email.user"), viper.GetString("email.password"), host)
	if err = c.Auth(auth); err != nil {
		return nil, err
	}

	return c, nil
}

// sendEmail is a helper function to send emails with a specific template
func sendEmail(data SendType, templateName string) error {
	from := mail.Address{Name: "BOOKSDUMP", Address: viper.GetString("email.from")}
	to := mail.Address{Address: data.Email}
	headers := map[string]string{
		"From":         from.String(),
		"To":           to.String(),
		"MIME-version": "1.0",
		"Content-Type": "text/html; charset=UTF-8",
		"Subject":      data.Subject,
	}

	var b bytes.Buffer
	for k, v := range headers {
		b.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	b.WriteString("\r\n")

	ss, err := MailConnection()
	if err != nil {
		logging.Errorf("Failed to establish email connection: %v", err)
		return err
	}
	defer ss.Quit()

	if err := ss.Mail(from.Address); err != nil || ss.Rcpt(to.Address) != nil {
		logging.Error(err)
		return err
	}

	w, err := ss.Data()
	if err != nil {
		logging.Error(err)
		return err
	}

	templatePath := fmt.Sprintf("email/templates/%s", templateName)
	asset, err := assets.Assets.ReadFile(templatePath)
	if err != nil {
		logging.Errorf("Failed to read email template %s: %v", templateName, err)
		return err
	}

	tpl, err := template.New(templateName).Parse(string(asset))
	if err != nil {
		logging.Errorf("Failed to parse email template %s: %v", templateName, err)
		return err
	}

	if err := tpl.ExecuteTemplate(&b, templateName, data); err != nil {
		logging.Errorf("Failed to execute email template %s: %v", templateName, err)
		return err
	}

	if _, err := w.Write(b.Bytes()); err != nil || w.Close() != nil {
		logging.Error(err)
		return err
	}

	logging.Infof("Email sent successfully to %s using template %s", data.Email, templateName)
	return nil
}

// SendActivationEmail sends registration confirmation email
func SendActivationEmail(data SendType) error {
	return sendEmail(data, "registration.gohtml")
}

// SendPasswordResetEmail sends password reset email
func SendPasswordResetEmail(data SendType) error {
	return sendEmail(data, "password_reset.gohtml")
}

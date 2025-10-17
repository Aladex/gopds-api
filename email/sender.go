package email

import (
	"bytes"
	"crypto/tls"
	"fmt"
	assets "gopds-api"
	"gopds-api/logging"
	"html/template"
	"net"
	"net/mail"
	"net/smtp"

	"github.com/spf13/viper"
)

type SendType struct {
	Title, Token, Button, Message, Email, Subject, Thanks string
}

func MailConnection() (*smtp.Client, error) {
	servername := viper.GetString("email.smtp_server")
	host, _, _ := net.SplitHostPort(servername)

	tlsconfig := &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         host,
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

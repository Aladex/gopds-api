package email

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"github.com/spf13/viper"
	"html/template"
	"log"
	"net"
	"net/mail"
	"net/smtp"
)

func init() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")

	err := viper.ReadInConfig() // Find and read the config file
	if err != nil {             // Handle errors reading the config file
		log.Fatalf("Fatal error config file: %s \n", err)
	}
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
		return c, err
	}
	return c, nil
}

func SendResetEmail(toEmail string) error {
	var b bytes.Buffer

	from := mail.Address{"Робот", "no-reply@booksdump.com"}
	to := mail.Address{"", toEmail}
	subj := "Сброс пароля"

	// Setup headers
	headers := make(map[string]string)
	headers["From"] = from.String()
	headers["To"] = to.String()
	headers["MIME-version"] = "1.0"
	headers["Content-Type"] = "text/html; charset=UTF-8"
	headers["Subject"] = subj

	// Setup message
	message := ""
	for k, v := range headers {
		message += fmt.Sprintf("%s: %s\r\n", k, v)
	}
	message += "\r\n"
	b.WriteString(message)
	ss, err := MailConnection()
	if err != nil {
		return err
	}
	servername := viper.GetString("email.smtp_server")
	host, _, _ := net.SplitHostPort(servername)
	auth := smtp.PlainAuth("",
		viper.GetString("email.user"),
		viper.GetString("email.password"),
		host)
	if err = ss.Auth(auth); err != nil {
		return err
	}
	// To && From
	if err = ss.Mail(from.Address); err != nil {
		return err
	}

	if err = ss.Rcpt(to.Address); err != nil {
		return err
	}

	// Data
	w, err := ss.Data()
	if err != nil {
		return err
	}

	tpl := template.Must(template.ParseGlob("templates/*"))
	err = tpl.ExecuteTemplate(&b, "reset_password.gohtml", "")
	if err != nil {
		return err
	}

	_, err = w.Write(b.Bytes())
	if err != nil {
		return err
	}

	err = w.Close()
	if err != nil {
		return err
	}

	err = ss.Quit()
	if err != nil {
		return err
	}
	return nil
}

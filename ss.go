package main

import (
	"bytes"
	"fmt"
	"github.com/spf13/viper"
	"gopds-api/email"
	"html/template"
	"log"
	"net"
	"net/mail"
	"net/smtp"
)

func main() {
	var b bytes.Buffer

	from := mail.Address{"Робот", "no-reply@booksdump.com"}
	to := mail.Address{"Таня", "aladex@gmail.com"}
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
	ss, err := email.MailConnection()
	if err != nil {
		log.Panicln(err)
	}
	servername := viper.GetString("email.smtp_server")
	host, _, _ := net.SplitHostPort(servername)
	auth := smtp.PlainAuth("",
		viper.GetString("email.user"),
		viper.GetString("email.password"),
		host)
	if err = ss.Auth(auth); err != nil {
		log.Panic(err)
	}
	// To && From
	if err = ss.Mail(from.Address); err != nil {
		log.Panic(err)
	}

	if err = ss.Rcpt(to.Address); err != nil {
		log.Panic(err)
	}

	// Data
	w, err := ss.Data()
	if err != nil {
		log.Panic(err)
	}

	tpl := template.Must(template.ParseGlob("email/templates/*"))
	err = tpl.ExecuteTemplate(&b, "reset_password.gohtml", "")
	if err != nil {
		log.Panicln(err)
	}

	_, err = w.Write(b.Bytes())
	if err != nil {
		log.Panic(err)
	}

	err = w.Close()
	if err != nil {
		log.Panic(err)
	}

	ss.Quit()

}

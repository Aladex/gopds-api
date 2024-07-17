package email

import (
	"bytes"
	"crypto/tls"
	"fmt"
	assets "gopds-api"
	"gopds-api/config"
	"gopds-api/logging"
	"html/template"
	"net"
	"net/mail"
	"net/smtp"
)

type SendType struct {
	Title   string
	Token   string
	Button  string
	Message string
	Email   string
	Subject string
	Thanks  string
}

func MailConnection() (*smtp.Client, error) {
	servername := config.AppConfig.GetString("email.smtp_server")
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

func SendActivationEmail(data SendType) error {
	var b bytes.Buffer

	from := mail.Address{"Робот", config.AppConfig.GetString("email.from")}
	to := mail.Address{"", data.Email}

	// Setup headers
	headers := make(map[string]string)
	headers["From"] = from.String()
	headers["To"] = to.String()
	headers["MIME-version"] = "1.0"
	headers["Content-Type"] = "text/html; charset=UTF-8"
	headers["Subject"] = data.Subject

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
	servername := config.AppConfig.GetString("email.smtp_server")
	host, _, _ := net.SplitHostPort(servername)
	auth := smtp.PlainAuth("",
		config.AppConfig.GetString("email.user"),
		config.AppConfig.GetString("email.password"),
		host)
	if err = ss.Auth(auth); err != nil {
		logging.CustomLog.Println(err)
		return err
	}

	if err = ss.Mail(from.Address); err != nil {
		logging.CustomLog.Println(err)
		return err
	}

	if err = ss.Rcpt(to.Address); err != nil {
		logging.CustomLog.Println(err)
		return err
	}

	w, err := ss.Data()
	if err != nil {
		logging.CustomLog.Println(err)
		return err
	}

	asset, err := assets.Assets.ReadFile("email/templates/reset_password.gohtml")
	if err != nil {
		logging.CustomLog.Println(err)
		return err
	}
	tpl, err := template.New("reset_password.gohtml").Parse(string(asset))
	if err != nil {
		logging.CustomLog.Println(err)
		return err
	}
	err = tpl.ExecuteTemplate(&b, "reset_password.gohtml", data)
	if err != nil {
		logging.CustomLog.Println(err)
		return err
	}

	_, err = w.Write(b.Bytes())
	if err != nil {
		logging.CustomLog.Println(err)
		return err
	}

	err = w.Close()
	if err != nil {
		logging.CustomLog.Println(err)
		return err
	}

	err = ss.Quit()
	if err != nil {
		logging.CustomLog.Println(err)
		return err
	}
	return nil
}

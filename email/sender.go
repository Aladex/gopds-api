package email

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"os"
	"time"

	"gopds-api/logging"

	"github.com/spf13/viper"
)

// SendType is one email's worth of content.
type SendType struct {
	Title   string
	Button  string
	Message string
	Email   string
	Subject string
	Thanks  string

	// URL is where the button goes and what the message offers to be copied.
	// It was called Token, which it never held: the value has always been a
	// full address, and the template has always used it as one.
	URL string

	// Warning is the caution shown above the signature, where one is
	// warranted. A registration needs none; a password reset does.
	Warning string

	// Footer says why this email arrived at all.
	Footer string

	// LinkFallback introduces the copyable address, for a client whose
	// buttons do not survive the trip.
	LinkFallback string

	// From is the address it is sent as, carried here so that composing a
	// message needs nothing but its argument.
	From string

	// ProductName is what this installation calls itself, shown as the
	// wordmark at the head of the email.
	ProductName string
}

// implicitTLSPort is the one port on which a server expects TLS before it says
// anything. Everywhere else the conversation starts in the clear and is lifted
// by STARTTLS.
const implicitTLSPort = "465"

// dialTimeout bounds reaching the mail server.
const dialTimeout = 15 * time.Second

// sessionTimeout bounds the whole conversation once the connection is up:
// greeting, STARTTLS, AUTH, the envelope, the body and QUIT. The dial timeout
// alone left everything after the TCP handshake unbounded, so a relay that
// accepted a connection and then said nothing held the caller forever — and a
// Kindle send holds one of a couple of global slots while it waits. Two
// minutes is generous for a 24 MiB body. A variable so tests can shorten it.
var sessionTimeout = 2 * time.Minute

// MailConnection opens an authenticated, encrypted session with the mail server.
//
// Two ports, two ways in. 465 wants TLS from the first byte. 587 — which is
// what MailerSend and most relays offer, and the only port MailerSend has —
// starts in the clear and is raised by STARTTLS. Only the first was supported,
// so a relay on 587 could not be reached at all.
//
// Either way the session is encrypted before the password is offered. A server
// that will not do STARTTLS is refused rather than obliged: sending PLAIN
// credentials over a connection anyone can read is worse than not sending the
// email.
func MailConnection() (*smtp.Client, error) {
	servername := viper.GetString("email.smtp_server")

	// The error was dropped here, which turned a misconfigured address into an
	// empty host: the certificate would then be checked against nothing, and
	// the SMTP AUTH would offer the password to whoever answered.
	host, port, err := net.SplitHostPort(servername)
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

	client, err := dial(servername, host, port, tlsconfig)
	if err != nil {
		return nil, err
	}

	auth := smtp.PlainAuth("", viper.GetString("email.user"), viper.GetString("email.password"), host)
	if err := client.Auth(auth); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("authenticating with %s: %w", host, err)
	}

	return client, nil
}

func dial(servername, host, port string, tlsconfig *tls.Config) (*smtp.Client, error) {
	// Bounded, because an unbounded one is how a mail server that stops
	// answering becomes a goroutine that never returns.
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()

	if port == implicitTLSPort {
		dialer := &tls.Dialer{Config: tlsconfig}
		conn, err := dialer.DialContext(ctx, "tcp", servername)
		if err != nil {
			return nil, fmt.Errorf("connecting to %s: %w", servername, err)
		}
		if deadlineErr := conn.SetDeadline(time.Now().Add(sessionTimeout)); deadlineErr != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("bounding the session with %s: %w", host, deadlineErr)
		}
		client, err := smtp.NewClient(conn, host)
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("starting a session with %s: %w", host, err)
		}
		return client, nil
	}

	plain, err := (&net.Dialer{}).DialContext(ctx, "tcp", servername)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", servername, err)
	}
	// Set on the raw connection, so it still holds once STARTTLS has wrapped
	// it: every read and write of the session goes through this socket.
	if deadlineErr := plain.SetDeadline(time.Now().Add(sessionTimeout)); deadlineErr != nil {
		_ = plain.Close()
		return nil, fmt.Errorf("bounding the session with %s: %w", host, deadlineErr)
	}
	client, err := smtp.NewClient(plain, host)
	if err != nil {
		_ = plain.Close()
		return nil, fmt.Errorf("starting a session with %s: %w", host, err)
	}
	if ok, _ := client.Extension("STARTTLS"); !ok {
		_ = client.Close()
		return nil, fmt.Errorf("%s offers no STARTTLS; refusing to send the password in the clear", servername)
	}
	if err := client.StartTLS(tlsconfig); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("starting TLS with %s: %w", host, err)
	}
	return client, nil
}

// mailConnection opens the session a message goes out over; tests point it at
// a server of their own.
var mailConnection = MailConnection

// sendEmail renders one email and hands it to the mail server.
func sendEmail(data *SendType, templateName string) error {
	data.From = viper.GetString("email.from")
	data.ProductName = productName()

	// Built before anything is dialed, so that a template this instance
	// cannot render costs nothing but an error.
	msg, err := buildMessage(data, templateName, time.Now())
	if err != nil {
		logging.Errorf("Failed to build email from template %s: %v", templateName, err)
		return err
	}

	if err := send(data.From, data.Email, msg); err != nil {
		logging.Errorf("Failed to send email to %s using template %s: %v", data.Email, templateName, err)
		return err
	}

	logging.Infof("Email sent successfully to %s using template %s", data.Email, templateName)
	return nil
}

// The stages of an SMTP conversation a send can fail at, as SendError names
// them.
const (
	StageConnect = "connect"
	StageMail    = "mail"
	StageRcpt    = "rcpt"
	StageData    = "data"
	StageAccept  = "accept"
	StageQuit    = "quit"
)

// SendError is a send that failed at one stage of the conversation.
//
// Its text carries no address. The stages used to be reported as "MAIL FROM
// <sender>" and "RCPT TO <recipient>", and a Kindle send logs its failures at
// error level: a refused recipient wrote the reader's Kindle address into the
// ordinary log. What the server said is still inside, and may itself echo an
// address — which is why FailureClass exists.
type SendError struct {
	Stage string
	Err   error
}

func (e *SendError) Error() string { return "smtp " + e.Stage + ": " + e.Err.Error() }
func (e *SendError) Unwrap() error { return e.Err }

// FailureClass describes a failed send by what it was rather than what anyone
// wrote: the stage, the server's reply code when it gave one, and whether the
// session ran out of time. Safe for a log at any level.
func FailureClass(err error) string {
	stage := "unknown"
	var sendErr *SendError
	if errors.As(err, &sendErr) {
		stage = sendErr.Stage
	}
	class := "stage=" + stage
	var reply *textproto.Error
	if errors.As(err, &reply) {
		class += fmt.Sprintf(" code=%d", reply.Code)
	}
	var netErr net.Error
	if errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
		class += " timeout"
	}
	return class
}

// send carries one built message through an SMTP conversation.
//
// Every step is checked on its own. They used to share a condition — `if
// err := c.Mail(from); err != nil || c.Rcpt(to) != nil` — which reported
// success whenever the second half was the half that failed, because the error
// it returned was the first half's nil. The same shape covered Write and
// Close, and Close is where the server finally says whether it took the
// message at all: a refused email was logged as delivered.
func send(from, to string, msg []byte) error {
	client, err := mailConnection()
	if err != nil {
		return &SendError{Stage: StageConnect, Err: err}
	}
	defer func() {
		// Quit closes the connection politely; a failure here — the server
		// gone quiet, the session deadline reached — says nothing about
		// whether the message was accepted, which Close already settled.
		if quitErr := client.Quit(); quitErr != nil {
			logging.Warnf("Closing the connection to the mail server: %s",
				FailureClass(&SendError{Stage: StageQuit, Err: quitErr}))
			_ = client.Close()
		}
	}()

	if mailErr := client.Mail(from); mailErr != nil {
		return &SendError{Stage: StageMail, Err: mailErr}
	}
	if rcptErr := client.Rcpt(to); rcptErr != nil {
		return &SendError{Stage: StageRcpt, Err: rcptErr}
	}

	w, err := client.Data()
	if err != nil {
		return &SendError{Stage: StageData, Err: err}
	}
	if _, writeErr := w.Write(msg); writeErr != nil {
		_ = w.Close()
		return &SendError{Stage: StageData, Err: writeErr}
	}
	// This is the acceptance, not a formality.
	if closeErr := w.Close(); closeErr != nil {
		return &SendError{Stage: StageAccept, Err: closeErr}
	}
	return nil
}

// SendActivationEmail sends registration confirmation email.
//
// The caller supplies the address and the link; every word comes from the
// configured language. It used to be the other way round — each caller read
// five strings out of the configuration and passed them in — which is why the
// two of them could and did drift apart.
func SendActivationEmail(data SendType) error {
	wording := resolve(kindRegistration)
	wording.apply(&data)
	return sendEmail(&data, notificationTemplate)
}

// SendPasswordResetEmail sends password reset email
func SendPasswordResetEmail(data SendType) error {
	wording := resolve(kindReset)
	wording.apply(&data)
	return sendEmail(&data, notificationTemplate)
}

// notificationTemplate is the only one there is; which email this is comes from
// the values handed to it.
const notificationTemplate = "notification.gohtml"

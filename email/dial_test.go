package email

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// MailerSend, which this installation is moving to, offers only port 587 with
// STARTTLS. The connection was implicit TLS and nothing else, so a relay on 587
// could not be reached at all — the dial would hang on a server waiting for a
// greeting it was never going to send in the clear.
//
// These run a mail server on a loopback port and check what the client does
// with it. Nothing here reaches the network.

// fakeServer speaks just enough SMTP to answer a greeting and an EHLO.
type fakeServer struct {
	offerSTARTTLS bool
	cert          tls.Certificate
	seen          []string
}

func (f *fakeServer) listen(t *testing.T) string {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		f.serve(conn)
	}()

	return listener.Addr().String()
}

func (f *fakeServer) serve(conn net.Conn) {
	reader := bufio.NewReader(conn)
	fmt.Fprint(conn, "220 fake ESMTP\r\n")

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		f.seen = append(f.seen, command)

		switch {
		case strings.HasPrefix(command, "EHLO"):
			if f.offerSTARTTLS {
				fmt.Fprint(conn, "250-fake\r\n250 STARTTLS\r\n")
			} else {
				fmt.Fprint(conn, "250 fake\r\n")
			}
		case command == "STARTTLS":
			fmt.Fprint(conn, "220 go ahead\r\n")
			tlsConn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{f.cert}})
			if err := tlsConn.HandshakeContext(context.Background()); err != nil {
				return
			}
			conn = tlsConn
			reader = bufio.NewReader(tlsConn)
		case strings.HasPrefix(command, "QUIT"):
			fmt.Fprint(conn, "221 bye\r\n")
			return
		default:
			fmt.Fprint(conn, "250 ok\r\n")
		}
	}
}

// A server that offers STARTTLS is taken up on it.
func TestDialRaisesAPlainConnectionWithSTARTTLS(t *testing.T) {
	cert, pool := selfSigned(t)
	server := &fakeServer{offerSTARTTLS: true, cert: cert}
	address := server.listen(t)

	host, port, _ := net.SplitHostPort(address)
	client, err := dial(address, host, port, &tls.Config{
		ServerName: "127.0.0.1",
		RootCAs:    pool,
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("dialing a STARTTLS server: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	var raised bool
	for _, command := range server.seen {
		if command == "STARTTLS" {
			raised = true
		}
	}
	if !raised {
		t.Errorf("the connection was never encrypted; the server saw %v", server.seen)
	}
}

// A server that does not offer it is refused rather than obliged: the next
// thing this code would do is send the mailbox password.
func TestDialRefusesAServerThatWillNotEncrypt(t *testing.T) {
	cert, _ := selfSigned(t)
	server := &fakeServer{offerSTARTTLS: false, cert: cert}
	address := server.listen(t)

	host, port, _ := net.SplitHostPort(address)
	client, err := dial(address, host, port, &tls.Config{MinVersion: tls.VersionTLS12})

	if err == nil {
		_ = client.Close()
		t.Fatal("a server offering no STARTTLS was accepted")
	}
	if !strings.Contains(err.Error(), "STARTTLS") {
		t.Errorf("the error does not say what was missing: %v", err)
	}
	for _, command := range server.seen {
		if strings.HasPrefix(command, "AUTH") {
			t.Fatal("credentials were offered over an unencrypted connection")
		}
	}
}

// 465 is the one port where the server expects TLS before it says anything.
func TestPortChoosesHowTheConnectionStarts(t *testing.T) {
	if implicitTLSPort != "465" {
		t.Errorf("implicit TLS is expected on 465, not %q", implicitTLSPort)
	}
}

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

// selfSigned makes a certificate the test's own client will accept.
//
// Generated here rather than written into the file: a baked-in certificate
// carries a fixed expiry, and a test that works until a date on the calendar
// passes is a test that fails one morning for no reason anybody changed.
func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}

	now := time.Now()
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "127.0.0.1"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating a certificate: %v", err)
	}

	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing the certificate just made: %v", err)
	}

	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

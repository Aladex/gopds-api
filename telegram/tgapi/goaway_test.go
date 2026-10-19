package tgapi_test

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gopds-api/telegram/tgapi"
	"gopds-api/telegram/tgtest"

	"github.com/go-telegram/bot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
)

// goAwayListener hands the first connection to a raw HTTP/2 peer that reads
// one whole request and then answers GOAWAY with last stream 0: "I processed
// nothing, retry elsewhere". Every later connection goes to the real server.
//
// That is what Telegram's frontends do while draining. Before v1.27.0 the SDK
// streamed request bodies through a pipe, http2.Transport could not replay a
// POST whose body it had already written, and every call on a draining
// connection failed while incoming updates kept arriving — the bot looked
// alive and answered nothing.
type goAwayListener struct {
	net.Listener
	srv     *httptest.Server
	goAways atomic.Int32
	done    atomic.Bool
}

func (l *goAwayListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil || l.done.Swap(true) {
		return conn, err
	}
	go l.serveGoAway(conn)
	return l.Listener.Accept()
}

func (l *goAwayListener) serveGoAway(raw net.Conn) {
	defer func() { _ = raw.Close() }()
	conn := tls.Server(raw, l.srv.TLS)
	if err := conn.HandshakeContext(context.Background()); err != nil {
		return
	}
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(conn, preface); err != nil {
		return
	}
	fr := http2.NewFramer(conn, conn)
	if err := fr.WriteSettings(); err != nil {
		return
	}
	for {
		f, err := fr.ReadFrame()
		if err != nil {
			return
		}
		switch f := f.(type) {
		case *http2.SettingsFrame:
			if !f.IsAck() {
				_ = fr.WriteSettingsAck()
			}
		case *http2.HeadersFrame:
			if f.StreamEnded() {
				l.goAway(fr)
				return
			}
		case *http2.DataFrame:
			if f.StreamEnded() {
				l.goAway(fr)
				return
			}
		}
	}
}

func (l *goAwayListener) goAway(fr *http2.Framer) {
	l.goAways.Add(1)
	_ = fr.WriteGoAway(0, http2.ErrCodeNo, nil)
	// Give the client time to read the frame before the connection closes.
	time.Sleep(100 * time.Millisecond)
}

func TestSendSurvivesGoAwayAfterTheBodyWasWritten(t *testing.T) {
	srv := httptest.NewUnstartedServer(nil)
	srv.EnableHTTP2 = true
	fake := tgtest.NewFakeBotAPIFromServer(t, srv)
	srv.Config.Handler = fake.Handler()
	listener := &goAwayListener{Listener: srv.Listener, srv: srv}
	srv.Listener = listener
	srv.StartTLS()
	t.Cleanup(srv.Close)

	b, err := tgapi.New(testToken, tgapi.Endpoint{ServerURL: srv.URL, HTTPClient: srv.Client()})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	msg, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: 4242, Text: "после GOAWAY"})
	require.NoError(t, err, "a POST on a draining HTTP/2 connection must be replayed on a new one")

	assert.Equal(t, int32(1), listener.goAways.Load(), "the first connection answered GOAWAY")
	calls := fake.Calls("sendMessage")
	require.Len(t, calls, 1, "the replay reached the server once")
	assert.Equal(t, "после GOAWAY", calls[0].Params["text"])
	assert.Equal(t, calls[0].MessageID, int64(msg.ID))
}

package objectstore_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"gopds-api/internal/objectstore"
	"gopds-api/internal/objectstore/s3test"
)

const (
	thumbsPrefix = "thumbs/"
	jpegType     = "image/jpeg"
)

func TestPutStoresASignedObject(t *testing.T) {
	server := s3test.New(t)
	client := server.WriterClient(timeout, thumbsPrefix)
	// The fake server recomputes the signature over the body it received.
	key := "thumbs/Толстой/книга +1.jpg"

	if err := client.Put(context.Background(), key, []byte("picture"), jpegType); err != nil {
		t.Fatal(err)
	}

	data, kind, ok := server.Object(key)
	if !ok || string(data) != "picture" || kind != jpegType {
		t.Fatalf("stored %q as %q, found: %v", data, kind, ok)
	}
}

func TestPutReplacesAnObject(t *testing.T) {
	server := s3test.New(t)
	client := server.WriterClient(timeout, thumbsPrefix)
	ctx := context.Background()
	for _, content := range []string{"old", "new"} {
		if err := client.Put(ctx, "thumbs/a.jpg", []byte(content), jpegType); err != nil {
			t.Fatal(err)
		}
	}
	if data, _, _ := server.Object("thumbs/a.jpg"); string(data) != "new" {
		t.Fatalf("holds %q", data)
	}
}

func TestPutOutsideThePrefixMakesNoRequest(t *testing.T) {
	server := s3test.New(t)
	client := server.WriterClient(timeout, thumbsPrefix)
	for _, key := range []string{
		"archives/a.zip",
		"covers/a.jpg",
		"thumbs",
		"thumbs/",
		"thumbsX/a.jpg",
		"thumbs/../archives/a.zip",
		"thumbs/./a.jpg",
		"thumbs//a.jpg",
		"/thumbs/a.jpg",
	} {
		err := client.Put(context.Background(), key, []byte("x"), jpegType)
		if !errors.Is(err, objectstore.ErrWriteRefused) {
			t.Errorf("%q: err = %v", key, err)
		}
	}
	if n := server.Requests(); n != 0 {
		t.Fatalf("%d requests left the client", n)
	}
}

func TestClientWithoutAPrefixCannotWrite(t *testing.T) {
	server := s3test.New(t)
	client := server.Client(timeout, -1)
	for _, key := range []string{"thumbs/a.jpg", "a.jpg", ""} {
		if err := client.Put(context.Background(), key, []byte("x"), jpegType); !errors.Is(err, objectstore.ErrWriteRefused) {
			t.Errorf("%q: err = %v", key, err)
		}
	}
	if n := server.Requests(); n != 0 {
		t.Fatalf("%d requests left the client", n)
	}
}

// Requirement 6: nothing that goes through this package can remove an object.
func TestClientHasNoWayToDelete(t *testing.T) {
	client := reflect.TypeOf(&objectstore.Client{})
	for i := range client.NumMethod() {
		name := strings.ToLower(client.Method(i).Name)
		if strings.Contains(name, "delete") || strings.Contains(name, "remove") {
			t.Errorf("client has %s", client.Method(i).Name)
		}
	}
}

func TestWriteFailuresLeaveTheReadBreakerAlone(t *testing.T) {
	server := s3test.New(t)
	server.Put("a", []byte("0123456789"))
	client := server.WriterClient(timeout, thumbsPrefix)
	ctx := context.Background()

	for name, breakIt := range map[string]func(bool){
		"refused": server.SetRefuseWrites,
		"failing": server.SetFailWrites,
		"hanging": server.SetHangWrites,
	} {
		breakIt(true)
		for range 7 {
			if err := client.Put(ctx, "thumbs/a.jpg", []byte("x"), jpegType); !errors.Is(err, objectstore.ErrUnavailable) {
				t.Fatalf("%s: err = %v", name, err)
			}
		}
		breakIt(false)
	}

	if err := readAll(ctx, client); err != nil {
		t.Fatalf("failed writes rested the reads: %v", err)
	}
}

func TestWriteGoesThroughWhileReadsRest(t *testing.T) {
	server := s3test.New(t)
	client := server.WriterClient(timeout, thumbsPrefix)
	ctx := context.Background()
	server.SetDown(true)
	for range 5 {
		_, _ = client.Head(ctx, "a")
	}
	server.SetDown(false)
	if _, err := client.Head(ctx, "a"); !errors.Is(err, objectstore.ErrUnavailable) {
		t.Fatalf("reads should be resting: %v", err)
	}
	if err := client.Put(ctx, "thumbs/a.jpg", []byte("x"), jpegType); err != nil {
		t.Fatalf("a write has nothing to do with the read breaker: %v", err)
	}
}

func TestHangingWriteIsBounded(t *testing.T) {
	server := s3test.New(t)
	server.SetHangWrites(true)
	client := server.WriterClient(timeout, thumbsPrefix)
	err := within(t, settled, func() error {
		return client.Put(context.Background(), "thumbs/a.jpg", []byte("x"), jpegType)
	})
	if !errors.Is(err, objectstore.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

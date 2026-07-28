package utils

import (
	"crypto/sha256"
	"strings"
	"testing"
)

// GetRandomString had no tests, which is a strange gap for the function that
// produces every password salt and activation token in the service. These pin
// the properties the rest of the code leans on: the alphabet, the length, and
// that two calls do not agree.

func TestGetRandomStringUsesOnlyTheAllowedAlphabet(t *testing.T) {
	// A salt lands in a $-separated hash string, so a stray separator would
	// split the record and CheckPbkdf2 would read the wrong segments. The
	// alphabet is what keeps that from happening.
	for _, length := range []int{1, 12, 64} {
		got := GetRandomString(length)
		for _, r := range got {
			if !strings.ContainsRune(allowedChars, r) {
				t.Fatalf("GetRandomString(%d) produced %q, outside the allowed alphabet", length, r)
			}
		}
	}
}

func TestGetRandomStringHonoursTheRequestedLength(t *testing.T) {
	for _, length := range []int{0, 1, 12, 64} {
		if got := GetRandomString(length); len(got) != length {
			t.Errorf("GetRandomString(%d) returned %d characters", length, len(got))
		}
	}
}

// Not a test of randomness — that is the business of crypto/rand — but of the
// wiring. A generator reset or seeded per call would show up here as repeats.
func TestGetRandomStringDoesNotRepeatItself(t *testing.T) {
	const (
		length = 16
		draws  = 200
	)
	seen := make(map[string]struct{}, draws)
	for i := 0; i < draws; i++ {
		s := GetRandomString(length)
		if _, dup := seen[s]; dup {
			t.Fatalf("GetRandomString(%d) returned %q twice in %d draws", length, s, draws)
		}
		seen[s] = struct{}{}
	}
}

// Every position must be able to vary. A loop that reused one draw for the
// whole string, or fixed a position, would pass the tests above and fail here.
func TestGetRandomStringVariesEveryPosition(t *testing.T) {
	const (
		length = 8
		draws  = 300
	)
	distinct := make([]map[byte]struct{}, length)
	for i := range distinct {
		distinct[i] = make(map[byte]struct{})
	}
	for i := 0; i < draws; i++ {
		s := GetRandomString(length)
		for pos := 0; pos < length; pos++ {
			distinct[pos][s[pos]] = struct{}{}
		}
	}
	for pos, values := range distinct {
		if len(values) < 2 {
			t.Errorf("position %d never changed across %d draws", pos, draws)
		}
	}
}

func TestCreatePasswordHashRoundTrips(t *testing.T) {
	const password = "correct horse battery staple"

	encoded := CreatePasswordHash(password)
	if encoded == "" {
		t.Fatal("CreatePasswordHash returned an empty string")
	}
	if parts := strings.SplitN(encoded, "$", 4); len(parts) != 4 {
		t.Fatalf("hash %q does not have the four segments CheckPbkdf2 expects", encoded)
	}

	ok, err := CheckPbkdf2(password, encoded, sha256.Size, sha256.New)
	if err != nil {
		t.Fatalf("checking a freshly made hash: %v", err)
	}
	if !ok {
		t.Error("a password did not match its own hash")
	}

	ok, err = CheckPbkdf2(password+" ", encoded, sha256.Size, sha256.New)
	if err != nil {
		t.Fatalf("checking a wrong password: %v", err)
	}
	if ok {
		t.Error("a wrong password matched the hash")
	}
}

// The salt is random, so the same password hashes differently every time. This
// is what stops one rainbow table from covering every account at once.
func TestCreatePasswordHashSaltsEachTime(t *testing.T) {
	const password = "same password"

	first := CreatePasswordHash(password)
	second := CreatePasswordHash(password)
	if first == second {
		t.Error("hashing the same password twice produced the same record")
	}

	// Both still verify: the salt travels inside the record.
	for _, encoded := range []string{first, second} {
		ok, err := CheckPbkdf2(password, encoded, sha256.Size, sha256.New)
		if err != nil || !ok {
			t.Errorf("hash %q did not verify: ok=%v err=%v", encoded, ok, err)
		}
	}
}

func TestCheckPbkdf2RejectsMalformedRecords(t *testing.T) {
	cases := map[string]string{
		"too few segments": "pbkdf2_sha256$100000$salt",
		"iterations not a number": "pbkdf2_sha256$many$salt$" +
			strings.Repeat("A", 44),
		"hash not base64": "pbkdf2_sha256$100000$salt$not base64!",
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			ok, err := CheckPbkdf2("whatever", encoded, sha256.Size, sha256.New)
			if err == nil {
				t.Error("expected an error, got none")
			}
			if ok {
				t.Error("a malformed record was accepted")
			}
		})
	}
}

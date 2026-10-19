// Package cbv2 is the callback_data of the bot's v2 buttons: a fixed-size
// envelope of what the button does and where it was shown, signed with the
// bot's navigation key and bound to the bot, its owner and the chat
// (plan.md §2.5).
//
// Wire form: "2." + base64url without padding of 45 bytes = 62 bytes, inside
// Telegram's 64.
//
//	offset  field       bytes
//	0       op          1
//	1       entity_id   8
//	9       view_id     8
//	17      page        3
//	20      arg         1
//	21      return_id   8
//	29      generation  2
//	31      revision    2
//	33      tag         12  HMAC-SHA256(key, "2." ‖ bytes[0:33] ‖ be64(bot) ‖ be64(owner) ‖ be64(chat))[:12]
package cbv2

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math"
	"strings"
)

// Op is what a button does.
type Op uint8

// The operations. Values are never reused or renumbered: a button sent today
// may be pressed after the next release.
const (
	OpHome Op = 1 + iota
	OpOpenBook
	OpOpenAuthor
	OpOpenSeries
	OpOpenCollection
	OpCollections
	OpFavorites
	OpPage
	OpTab
	OpBack
	OpDownload
	OpSetFavorite
	OpAnnotation
	OpCover
	OpBookAuthors
	OpPromptAuthor
	OpClearAuthor
	OpPromptAuthorSearch
	OpPromptFavSearch
	OpPickAuthor
	OpCancelPrompt
	OpLanguagePicker
	OpSetLanguage
	OpSuggestions
	OpRetrySearch
	OpRetryJob
	OpSettings
	OpSetFormat
	OpHelp
	OpFormatHelp
	OpDonate
	OpConnection
	// Appended by plan-ui.md.
	OpCardStyle
	OpSetCardStyle
	OpInlineHelp
	OpInlineCheck
	OpAnnotationPage
	OpRetryBranch
	opEnd
)

// DocumentRevision marks a button on a document: documents are not panels
// and have no revision to compare.
const DocumentRevision = math.MaxUint16

// Envelope is a decoded button.
type Envelope struct {
	Op         Op
	EntityID   uint64
	ViewID     uint64
	Page       uint32
	Arg        uint8
	ReturnID   uint64
	Generation uint16
	Revision   uint16
}

// BindCtx is what a button is bound to besides its key: a button copied to
// another bot, owner or chat does not verify.
type BindCtx struct{ TGBotID, OwnerTGID, ChatID int64 }

// Errors of Decode, in the order they are checked.
var (
	ErrVersion   = errors.New("cbv2: not a v2 button")
	ErrMalformed = errors.New("cbv2: malformed button")
	ErrTag       = errors.New("cbv2: button tag does not verify")
)

const (
	prefix   = "2."
	bodyLen  = 33
	tagLen   = 12
	wireLen  = bodyLen + tagLen
	maxPage  = 1<<24 - 1
	maxInt64 = math.MaxInt64
	pageHigh = 16
	pageMid  = 8
)

// encodedLen is the wire length of a button.
var encodedLen = len(prefix) + base64.RawURLEncoding.EncodedLen(wireLen)

// Encode signs e for c. Out-of-range fields are an error, never truncated.
func Encode(e *Envelope, key []byte, c BindCtx) (string, error) {
	if e.Op == 0 || e.Op >= opEnd {
		return "", ErrMalformed
	}
	if e.Page > maxPage || e.EntityID > maxInt64 || e.ViewID > maxInt64 || e.ReturnID > maxInt64 {
		return "", ErrMalformed
	}
	var b [wireLen]byte
	b[0] = byte(e.Op)
	binary.BigEndian.PutUint64(b[1:9], e.EntityID)
	binary.BigEndian.PutUint64(b[9:17], e.ViewID)
	b[17], b[18], b[19] = byte(e.Page>>pageHigh), byte(e.Page>>pageMid), byte(e.Page) // #nosec G115 -- the three bytes of a 24-bit page
	b[20] = e.Arg
	binary.BigEndian.PutUint64(b[21:29], e.ReturnID)
	binary.BigEndian.PutUint16(b[29:31], e.Generation)
	binary.BigEndian.PutUint16(b[31:33], e.Revision)
	copy(b[bodyLen:], tag(b[:bodyLen], key, c))
	return prefix + base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// Decode verifies s for c and returns its envelope.
func Decode(s string, key []byte, c BindCtx) (Envelope, error) {
	if !strings.HasPrefix(s, prefix) {
		return Envelope{}, ErrVersion
	}
	if len(s) != encodedLen {
		return Envelope{}, ErrMalformed
	}
	b, err := base64.RawURLEncoding.DecodeString(s[len(prefix):])
	if err != nil || len(b) != wireLen {
		return Envelope{}, ErrMalformed
	}
	if !hmac.Equal(b[bodyLen:], tag(b[:bodyLen], key, c)) {
		return Envelope{}, ErrTag
	}
	e := Envelope{
		Op:         Op(b[0]),
		EntityID:   binary.BigEndian.Uint64(b[1:9]),
		ViewID:     binary.BigEndian.Uint64(b[9:17]),
		Page:       uint32(b[17])<<pageHigh | uint32(b[18])<<pageMid | uint32(b[19]),
		Arg:        b[20],
		ReturnID:   binary.BigEndian.Uint64(b[21:29]),
		Generation: binary.BigEndian.Uint16(b[29:31]),
		Revision:   binary.BigEndian.Uint16(b[31:33]),
	}
	if e.Op == 0 || e.Op >= opEnd || e.EntityID > maxInt64 || e.ViewID > maxInt64 || e.ReturnID > maxInt64 {
		return Envelope{}, ErrMalformed
	}
	return e, nil
}

// IsV2 reports whether data looks like a v2 button, signed or not.
func IsV2(data string) bool { return strings.HasPrefix(data, prefix) }

func tag(body, key []byte, c BindCtx) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(prefix))
	mac.Write(body)
	var ids [24]byte
	binary.BigEndian.PutUint64(ids[0:8], uint64(c.TGBotID))    // #nosec G115 -- ids are bit patterns here
	binary.BigEndian.PutUint64(ids[8:16], uint64(c.OwnerTGID)) // #nosec G115 -- ids are bit patterns here
	binary.BigEndian.PutUint64(ids[16:24], uint64(c.ChatID))   // #nosec G115 -- ids are bit patterns here
	mac.Write(ids[:])
	return mac.Sum(nil)[:tagLen]
}

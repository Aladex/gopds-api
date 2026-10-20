package tgtest

import (
	"net/http"
	"strings"
)

// The look's methods, as the fake matches them (lower case).
const (
	methodSetMyDescription      = "setmydescription"
	methodSetMyShortDescription = "setmyshortdescription"
	methodSetMyProfilePhoto     = "setmyprofilephoto"
	methodGetMyDescription      = "getmydescription"
	methodGetMyShortDescription = "getmyshortdescription"
)

// Profile is what a bot was given as its look.
type Profile struct {
	Description      string
	ShortDescription string
	// Photo is the uploaded profile photo, nil until one is set.
	Photo *UploadedFile
}

// ProfileOf is the bot of token's look as Telegram keeps it; nil when the
// bot was given none.
func (f *FakeBotAPI) ProfileOf(token string) *Profile {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.profiles[token]; ok {
		copied := *p
		return &copied
	}
	return nil
}

// profileReply keeps and answers a bot's descriptions and profile photo.
func (f *FakeBotAPI) profileReply(w http.ResponseWriter, c *Call) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.profiles == nil {
		f.profiles = map[string]*Profile{}
	}
	p := f.profiles[c.Token]
	if p == nil {
		p = &Profile{}
	}
	switch strings.ToLower(c.Method) {
	case methodSetMyDescription:
		p.Description = c.Params["description"]
	case methodSetMyShortDescription:
		p.ShortDescription = c.Params["short_description"]
	case methodSetMyProfilePhoto:
		var photo struct {
			Type  string `json:"type"`
			Photo string `json:"photo"`
		}
		if err := c.Decode("photo", &photo); err != nil || photo.Type != "static" {
			writeError(w, http.StatusBadRequest, "Bad Request: can't parse InputProfilePhoto")
			return
		}
		part, ok := c.Files[strings.TrimPrefix(photo.Photo, "attach://")]
		if !ok {
			writeError(w, http.StatusBadRequest, "Bad Request: photo not found")
			return
		}
		p.Photo = &part
	case methodGetMyDescription:
		writeResult(w, map[string]string{"description": p.Description})
		return
	case methodGetMyShortDescription:
		writeResult(w, map[string]string{"short_description": p.ShortDescription})
		return
	}
	f.profiles[c.Token] = p
	writeResult(w, true)
}

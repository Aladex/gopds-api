package tgtest

import "fmt"

// documentFileID is the file_id an upload gets: like Telegram's, it differs
// between bots for the same file.
func documentFileID(token string, doc UploadedFile) string {
	return fmt.Sprintf("doc-%d-%s", botID(token), doc.SHA256[:16])
}

// document records an uploaded document's file_id as the bot's, or checks
// that a document sent by file_id is one the bot uploaded and still has.
func (f *FakeBotAPI) document(c *Call) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.files == nil {
		f.files = map[string]map[string]bool{}
	}
	if doc, ok := c.Files["document"]; ok {
		if f.files[c.Token] == nil {
			f.files[c.Token] = map[string]bool{}
		}
		f.files[c.Token][documentFileID(c.Token, doc)] = true
		return true
	}
	return f.files[c.Token][c.Params["document"]]
}

// ForgetFiles makes Telegram forget every file the bot of token uploaded:
// sent by its file_id again, a file is refused.
func (f *FakeBotAPI) ForgetFiles(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.files, token)
}

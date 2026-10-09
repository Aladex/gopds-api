package api

import (
	"slices"
	"strings"
	"sync"
	"time"

	"gopds-api/database"
	"gopds-api/services"
)

// authorSourceFailures keeps the per-book author source failures of the paths
// that refresh existing books outside an archive scan — the fix scan and an
// approved single-book rescan — for the shared scan errors list. An archive
// scan's own failures travel in its report, like its other errors.
//
// Like the archive scan's errors it lives in memory: the newest
// maxAuthorSourceFailures entries; a new fix scan replaces the previous fix
// scan's entries.
type authorSourceFailureList struct {
	mu    sync.Mutex
	items []authorSourceFailure
}

type authorSourceFailure struct {
	entry   ScanErrorResponse
	fromFix bool
}

const maxAuthorSourceFailures = 500

var authorSourceFailures authorSourceFailureList

// closedAuthorSourceClass closes a refresh class at the response boundary:
// the run vocabulary and the refresh's own "unavailable" pass, anything else
// reads as unavailable.
func closedAuthorSourceClass(class string) string {
	if class == services.AuthorMetadataRefreshUnavailable ||
		slices.Contains(database.AuthorMetadataScanFailureClasses(), class) {
		return class
	}
	return services.AuthorMetadataRefreshUnavailable
}

func (l *authorSourceFailureList) record(archive, entry, class string, fromFix bool, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append(l.items, authorSourceFailure{
		entry: ScanErrorResponse{
			FileName:    entry,
			ArchiveName: archive,
			Error:       services.AuthorMetadataErrorPrefix + closedAuthorSourceClass(class),
			Timestamp:   at,
		},
		fromFix: fromFix,
	})
	if extra := len(l.items) - maxAuthorSourceFailures; extra > 0 {
		l.items = slices.Delete(l.items, 0, extra)
	}
}

// startFixScan drops the previous fix scan's entries.
func (l *authorSourceFailureList) startFixScan() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = slices.DeleteFunc(l.items, func(f authorSourceFailure) bool { return f.fromFix })
}

func (l *authorSourceFailureList) list() []ScanErrorResponse {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]ScanErrorResponse, 0, len(l.items))
	for i := range l.items {
		out = append(out, l.items[i].entry)
	}
	return out
}

// recordFixScanAuthorFailures adds the fix scan's per-book author source
// failures to the list: the report's own bounded set of them, which legacy
// failures earlier in the run cannot crowd out.
func recordFixScanAuthorFailures(report *services.FixScanReport, at time.Time) {
	if report == nil {
		return
	}
	for _, e := range report.AuthorSourceFailures {
		if class, ok := strings.CutPrefix(e.Error, services.AuthorMetadataErrorPrefix); ok {
			authorSourceFailures.record(e.ArchivePath, e.FileName, class, true, at)
		}
	}
}

func resetAuthorSourceFailures() {
	authorSourceFailures.mu.Lock()
	defer authorSourceFailures.mu.Unlock()
	authorSourceFailures.items = nil
}

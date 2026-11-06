package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/internal/archivestore"
	"gopds-api/logging"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// Answers of the intake scan: what became of the one archive it names.
const (
	IntakeScanStarted = "started"
	IntakeScanRunning = "running"
	IntakeScanScanned = "scanned"
	// IntakeScanIdle answers check_only: not scanned, and nothing running.
	IntakeScanIdle = "idle"
)

// intakeTokenMinLength keeps a placeholder from opening a public route.
const intakeTokenMinLength = 32

// IntakeScanRequest names one archive, by its file name. CheckOnly asks how
// its scan stands and starts nothing.
type IntakeScanRequest struct {
	Name      string `json:"name" binding:"required"`
	CheckOnly bool   `json:"check_only"`
}

// IntakeScanResponse says what the archive's scan is at. The two counts come
// with "scanned" only.
type IntakeScanResponse struct {
	Status      string `json:"status"`
	SessionID   string `json:"session_id,omitempty"`
	BooksCount  int    `json:"books_count,omitempty"`
	ErrorsCount int    `json:"errors_count,omitempty"`
}

// SetupIntakeRoutes sets up the routes of the intake job, which holds a token
// instead of a session. The route is absent in effect until a token is
// configured.
func SetupIntakeRoutes(r *gin.RouterGroup) {
	r.POST("/scan", requireIntakeToken, IntakeScan)
}

// intakeToken is the configured token, or nothing when intake is off.
func intakeToken() string {
	token := strings.TrimSpace(viper.GetString("scanning.intake_token"))
	if len(token) < intakeTokenMinLength {
		return ""
	}
	return token
}

// requireIntakeToken answers 404 with no token configured, so an installation
// that never set one has no such route, and 401 for a wrong one.
func requireIntakeToken(c *gin.Context) {
	token := intakeToken()
	if token == "" {
		httputil.NewError(c, http.StatusNotFound, errors.New("not found"))
		c.Abort()
		return
	}
	given := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	// Hashing first keeps the comparison from depending on the lengths.
	want, got := sha256.Sum256([]byte(token)), sha256.Sum256([]byte(given))
	if subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
		httputil.NewError(c, http.StatusUnauthorized, errors.New("invalid token"))
		c.Abort()
		return
	}
	c.Next()
}

// intakeArchiveName accepts a zip file directly under the archives directory.
func intakeArchiveName(name string) bool {
	return name == filepath.Base(name) && strings.HasSuffix(strings.ToLower(name), ".zip")
}

// IntakeScan godoc
// @Summary Scan one new archive
// @Description Starts a scan of one archive that was never scanned, and only
// @Description that: an archive already scanned is never read again, because a
// @Description second pass over it refreshes the author layer of its books.
// @Description Called again it reports how the scan went, and with check_only
// @Description it starts nothing, so the intake job needs no other route.
// @Tags intake
// @Param Authorization header string true "Bearer intake token"
// @Accept json
// @Produce json
// @Param body body IntakeScanRequest true "Archive file name"
// @Success 200 {object} IntakeScanResponse "Already scanned"
// @Success 202 {object} IntakeScanResponse "Scan started"
// @Failure 400 {object} httputil.HTTPError
// @Failure 401 {object} httputil.HTTPError
// @Failure 404 {object} httputil.HTTPError "No such archive, or no token configured"
// @Failure 409 {object} IntakeScanResponse "A scan is running"
// @Failure 500 {object} httputil.HTTPError
// @Router /api/intake/scan [post]
func IntakeScan(c *gin.Context) {
	var req IntakeScanRequest
	if err := c.ShouldBindJSON(&req); err != nil || !intakeArchiveName(req.Name) {
		httputil.NewError(c, http.StatusBadRequest, errors.New("archive file name required"))
		return
	}

	catalog, err := database.GetScanStatus(req.Name)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, errors.New("scan status unavailable"))
		return
	}
	if catalog != nil && catalog.IsScanned {
		c.JSON(http.StatusOK, IntakeScanResponse{Status: IntakeScanScanned, BooksCount: catalog.BooksCount,
			ErrorsCount: catalog.ErrorsCount})
		return
	}

	if fixState.isRunning() || scanState.isRunning() {
		c.JSON(http.StatusConflict, IntakeScanResponse{Status: IntakeScanRunning})
		return
	}
	if req.CheckOnly {
		c.JSON(http.StatusOK, IntakeScanResponse{Status: IntakeScanIdle})
		return
	}

	archivePath, err := resolveArchivePath(req.Name)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	if _, _, err := archivestore.Default().Stat(c.Request.Context(), archivePath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			httputil.NewError(c, http.StatusNotFound, errors.New("archive not found"))
			return
		}
		httputil.BookFileError(c, err)
		return
	}

	sessionID := fmt.Sprintf("intake_%d", time.Now().UnixNano())
	if !scanState.tryStart(sessionID, time.Now()) {
		c.JSON(http.StatusConflict, IntakeScanResponse{Status: IntakeScanRunning})
		return
	}
	logging.Infof("Intake scan of %s started", req.Name)
	go runSingleArchiveScan(sessionID, archivePath)

	c.JSON(http.StatusAccepted, IntakeScanResponse{Status: IntakeScanStarted, SessionID: sessionID})
}

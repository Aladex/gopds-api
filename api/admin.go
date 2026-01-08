package api

import (
	"errors"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"gopds-api/services"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// SetupAdminRoutes sets up the admin routes
func SetupAdminRoutes(r *gin.RouterGroup) {
	r.POST("/users", GetUsers)
	r.GET("/invites", GetInvites)
	r.POST("/invite", ChangeInvite)
	r.POST("/user", ActionUser)
	r.DELETE("/user/:id", DeleteUser)
	r.POST("/update-book", UpdateBook)
	r.PUT("/books/:id", UpdateBookByID)
	r.GET("/authors/search", SearchAuthors)
	r.GET("/series/search", SearchSeries)

	// Book rescan routes
	r.POST("/books/:id/rescan", RescanBookPreview)
	r.GET("/books/:id/rescan/preview-cover", RescanPreviewCover)
	r.POST("/books/:id/rescan/approve", ApproveRescan)

	// Book scan routes
	r.POST("/scan", StartScan)
	r.GET("/scan/status", GetScanStatus)
	r.GET("/scan/errors", GetScanErrors)
	r.GET("/scan/errors/file", GetScanErrorFile)
	r.GET("/scan/unscanned", GetUnscannedArchives)
	r.GET("/scan/scanned", GetScannedArchives)
	r.POST("/scan/archive", ScanSpecificArchive)
	r.DELETE("/scan/reset/:name", ResetArchiveScanStatus)

	// Setup duplicate management routes
	SetupDuplicatesRoutes(r)
}

// UsersAnswer struct for users list in admin space
type UsersAnswer struct {
	Users  []models.User `json:"users"`
	Length int           `json:"length"`
}

// GetUsers method for fetching the list of users in the admin space
// Auth godoc
// @Summary Retrieve the list of users
// @Description Get a list of users for the admin space
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param  body body models.UserFilters true "User filters"
// @Success 200 {object} UsersAnswer
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/users [post]
func GetUsers(c *gin.Context) {
	var filters models.UserFilters
	if err := c.ShouldBindJSON(&filters); err == nil {
		users, count, err := database.GetUserList(filters)
		if err != nil {
			c.JSON(500, err)
			return
		}
		lenght := count / 50
		if count-lenght*50 > 0 {
			lenght++
		}
		c.JSON(200, UsersAnswer{
			Users:  users,
			Length: lenght,
		})
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad request"))
}

// GetInvites method for retrieving the list of invites
// Auth godoc
// @Summary Retrieve the list of invites
// @Description Get a list of invites
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Success 200 {object} models.Result
// @Failure 500 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Router /api/admin/invites [get]
func GetInvites(c *gin.Context) {
	invites := []models.Invite{}
	err := database.GetInvites(&invites)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(200, models.Result{
		Result: invites,
		Error:  nil,
	})
}

// ChangeInvite method for changing or creating an invite
// Auth godoc
// @Summary Change or create an invite
// @Description Change or create an invite
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param  body body models.InviteRequest true "Invite parameters"
// @Success 200 {object} models.Result
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/invite [post]
func ChangeInvite(c *gin.Context) {
	var inviteRequest models.InviteRequest
	if err := c.ShouldBindJSON(&inviteRequest); err == nil {
		err := database.ChangeInvite(inviteRequest)
		if err != nil {
			httputil.NewError(c, http.StatusInternalServerError, err)
			return
		}
		c.JSON(200, models.Result{
			Result: "result_ok",
			Error:  nil,
		})
		return
	}
	httputil.NewError(c, http.StatusBadRequest, errors.New("bad_request"))
}

// UpdateBook updates the information of a specific book.
// @Summary Update book information
// @Description Update the information of a specific book based on the provided JSON payload. Only provided fields will be updated.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept json
// @Produce json
// @Param body body models.BookUpdateRequest true "Book update information"
// @Success 200 {object} models.Result "Book updated successfully"
// @Failure 400 {object} httputil.HTTPError "Bad request - invalid input parameters"
// @Failure 404 {object} httputil.HTTPError "Book not found"
// @Failure 500 {object} httputil.HTTPError "Internal server error"
// @Router /api/admin/update-book [post]
func UpdateBook(c *gin.Context) {
	var updateReq models.BookUpdateRequest
	if err := c.ShouldBindJSON(&updateReq); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_request_body"))
		return
	}

	// Update the book in database
	updatedBook, err := database.UpdateBook(updateReq)
	if err != nil {
		// Check if book was not found
		if err.Error() == "pg: no rows in result set" {
			httputil.NewError(c, http.StatusNotFound, errors.New("book_not_found"))
			return
		}
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	// Return the updated book
	c.JSON(http.StatusOK, models.Result{
		Result: updatedBook,
		Error:  nil,
	})
}

// UpdateBookByID updates the information of a specific book using URL parameter.
// @Summary Update book information by ID
// @Description Update the information of a specific book by ID based on the provided JSON payload. Only provided fields will be updated.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Book ID"
// @Accept json
// @Produce json
// @Param body body models.BookUpdateRequest true "Book update information"
// @Success 200 {object} models.Result "Book updated successfully"
// @Failure 400 {object} httputil.HTTPError "Bad request - invalid input parameters"
// @Failure 404 {object} httputil.HTTPError "Book not found"
// @Failure 500 {object} httputil.HTTPError "Internal server error"
// @Router /api/admin/books/{id} [put]
func UpdateBookByID(c *gin.Context) {
	bookIDStr := c.Param("id")
	bookID, err := strconv.ParseInt(bookIDStr, 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_book_id"))
		return
	}

	var updateReq models.BookUpdateRequest
	if err := c.ShouldBindJSON(&updateReq); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_request_body"))
		return
	}

	// Set the ID from URL parameter
	updateReq.ID = bookID

	// Update the book in database
	updatedBook, err := database.UpdateBook(updateReq)
	if err != nil {
		// Check if book was not found
		if err.Error() == "pg: no rows in result set" {
			httputil.NewError(c, http.StatusNotFound, errors.New("book_not_found"))
			return
		}
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	// Return the updated book
	c.JSON(http.StatusOK, models.Result{
		Result: updatedBook,
		Error:  nil,
	})
}

// RescanBookPreview godoc
// @Summary Preview book rescan changes
// @Description Parse FB2 file and preview what will change without applying changes
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Book ID"
// @Produce json
// @Success 200 {object} models.RescanPreview "Preview of changes"
// @Failure 400 {object} httputil.HTTPError "Bad request"
// @Failure 404 {object} httputil.HTTPError "Book not found"
// @Failure 500 {object} httputil.HTTPError "Internal server error"
// @Router /api/admin/books/{id}/rescan [post]
func RescanBookPreview(c *gin.Context) {
	bookIDStr := c.Param("id")
	bookID, err := strconv.ParseInt(bookIDStr, 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_book_id"))
		return
	}

	// Get user from context (set by auth middleware)
	userIDVal, exists := c.Get("user_id")
	if !exists {
		httputil.NewError(c, http.StatusUnauthorized, errors.New("user_not_found"))
		return
	}
	userID, ok := userIDVal.(int64)
	if !ok {
		httputil.NewError(c, http.StatusUnauthorized, errors.New("invalid_user_id"))
		return
	}

	// Get archives directory from config
	archivesDir := viper.GetString("app.files_path")
	if archivesDir == "" {
		archivesDir = "./files/" // Default fallback
	}

	coversDir := viper.GetString("app.posters_path")
	if coversDir == "" {
		coversDir = "./posters/" // Default fallback
	}

	// Create language detector
	enableDetection, enableOpenAI, openaiTimeout := getLanguageDetectionSettings()
	var languageDetector *services.LanguageDetector
	if enableDetection {
		languageDetector = services.NewLanguageDetector(enableOpenAI, openaiTimeout)
	}

	// Create rescan service
	rescanService := services.NewRescanService(archivesDir, coversDir, languageDetector)

	// Generate preview
	preview, err := rescanService.RescanBookPreview(bookID, userID)
	if err != nil {
		if err.Error() == "book not found" {
			httputil.NewError(c, http.StatusNotFound, err)
		} else {
			httputil.NewError(c, http.StatusInternalServerError, err)
		}
		return
	}

	c.JSON(http.StatusOK, models.Result{
		Result: preview,
		Error:  nil,
	})
}

// RescanPreviewCover godoc
// @Summary Get rescan cover preview
// @Description Returns the extracted cover image from pending rescan
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Book ID"
// @Produce image/jpeg
// @Success 200 {file} file "Cover image"
// @Failure 404 {object} httputil.HTTPError "Cover not found"
// @Failure 500 {object} httputil.HTTPError "Internal server error"
// @Router /api/admin/books/{id}/rescan/preview-cover [get]
func RescanPreviewCover(c *gin.Context) {
	bookIDStr := c.Param("id")
	bookID, err := strconv.ParseInt(bookIDStr, 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_book_id"))
		return
	}

	pending, err := database.GetRescanPendingByBookID(bookID)
	if err != nil {
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}
	if pending == nil || !pending.CoverUpdated || len(pending.CoverData) == 0 {
		httputil.NewError(c, http.StatusNotFound, errors.New("cover_not_found"))
		return
	}

	contentType := http.DetectContentType(pending.CoverData)
	c.Data(http.StatusOK, contentType, pending.CoverData)
}

// ApproveRescan godoc
// @Summary Approve or reject book rescan
// @Description Apply or reject pending book rescan changes. When approving, you can selectively choose which fields to update.
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Book ID"
// @Accept json
// @Produce json
// @Param body body models.RescanApprovalRequest true "Approval action with optional field selection"
// @Success 200 {object} models.RescanApprovalResponse "Approval result with updated/skipped fields"
// @Failure 400 {object} httputil.HTTPError "Bad request"
// @Failure 404 {object} httputil.HTTPError "Book not found"
// @Failure 500 {object} httputil.HTTPError "Internal server error"
// @Router /api/admin/books/{id}/rescan/approve [post]
func ApproveRescan(c *gin.Context) {
	bookIDStr := c.Param("id")
	bookID, err := strconv.ParseInt(bookIDStr, 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_book_id"))
		return
	}

	var approvalReq models.RescanApprovalRequest
	if err := c.ShouldBindJSON(&approvalReq); err != nil {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_request_body"))
		return
	}

	// Validate action
	if approvalReq.Action != "approve" && approvalReq.Action != "reject" {
		httputil.NewError(c, http.StatusBadRequest, errors.New("invalid_action"))
		return
	}

	// If action is "approve", set defaults for any unspecified fields
	if approvalReq.Action == "approve" {
		approvalReq.SetDefaults()
	}

	// Get archives directory from config
	archivesDir := viper.GetString("app.files_path")
	if archivesDir == "" {
		archivesDir = "./files/" // Default fallback
	}

	coversDir := viper.GetString("app.posters_path")
	if coversDir == "" {
		coversDir = "./posters/" // Default fallback
	}

	// Create language detector (not used in approve/reject, but required by constructor)
	languageDetector := services.NewLanguageDetector(false, 5*time.Second)

	// Create rescan service
	rescanService := services.NewRescanService(archivesDir, coversDir, languageDetector)

	var response *models.RescanApprovalResponse
	if approvalReq.Action == "approve" {
		response, err = rescanService.ApproveRescan(bookID, &approvalReq)
	} else {
		response, err = rescanService.RejectRescan(bookID)
	}

	if err != nil {
		if err.Error() == "book not found" || err.Error() == "no pending rescan found for this book" {
			httputil.NewError(c, http.StatusNotFound, err)
		} else {
			httputil.NewError(c, http.StatusInternalServerError, err)
		}
		return
	}

	c.JSON(http.StatusOK, models.Result{
		Result: response,
		Error:  nil,
	})
}

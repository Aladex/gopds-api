package api

import (
	"context"
	"fmt"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/logging"
	"gopds-api/models"
	"gopds-api/services"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-pg/pg/v10"
	"github.com/spf13/viper"
)

// Global WebSocket manager for admin notifications
var wsManager *services.WebSocketManager

// InitWebSocketManager initializes the WebSocket manager
func InitWebSocketManager() {
	wsManager = services.NewWebSocketManager()
	logging.Info("WebSocket manager initialized")
}

// SetupDuplicatesRoutes sets up the duplicates routes
func SetupDuplicatesRoutes(r *gin.RouterGroup) {
	r.POST("/duplicates/scan", StartDuplicateScan)
	r.GET("/duplicates/scan/:id", GetScanJobStatus)
	r.GET("/duplicates", GetDuplicateGroups)
	r.POST("/duplicates/hide", HideDuplicates)
}

// ScanJobResponse represents the response when starting a scan
type ScanJobResponse struct {
	JobID int64 `json:"job_id"`
}

// StartDuplicateScan starts a new duplicate scan job
// @Summary Start duplicate scan
// @Description Start a new duplicate book scan job (async)
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept json
// @Produce json
// @Success 200 {object} ScanJobResponse
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 409 {object} httputil.HTTPError "Scan already running"
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/duplicates/scan [post]
func StartDuplicateScan(c *gin.Context) {
	db := getDB()
	ctx := context.Background()

	// Check if a scan is already running
	err := services.EnsureOneScanRunning(ctx, db)
	if err != nil {
		logging.Warnf("Scan already running: %v", err)
		httputil.NewError(c, http.StatusConflict, fmt.Errorf("a scan is already running"))
		return
	}

	// Create a new scan job
	job := &models.AdminScanJob{
		Status:     "pending",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		ScanParams: `{"type": "full_scan"}`,
	}

	_, err = db.Model(job).Insert()
	if err != nil {
		logging.Errorf("Failed to create scan job: %v", err)
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	logging.Infof("Created scan job %d", job.ID)

	// Start scan in background
	filesPath := viper.GetString("app.files_path")
	wsConn := services.NewAdminWSConnection(wsManager)

	go func() {
		scanCtx := context.Background()
		err := services.ScanDuplicates(scanCtx, db, job.ID, wsConn, filesPath)
		if err != nil {
			logging.Errorf("Scan job %d failed: %v", job.ID, err)
		}
	}()

	c.JSON(http.StatusOK, ScanJobResponse{
		JobID: job.ID,
	})
}

// GetScanJobStatus retrieves the status of a scan job
// @Summary Get scan job status
// @Description Get the status of a duplicate scan job
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Scan Job ID"
// @Accept json
// @Produce json
// @Success 200 {object} models.AdminScanJob
// @Failure 400 {object} httputil.HTTPError
// @Failure 403 {object} httputil.HTTPError
// @Failure 404 {object} httputil.HTTPError
// @Router /api/admin/duplicates/scan/{id} [get]
func GetScanJobStatus(c *gin.Context) {
	jobIDStr := c.Param("id")
	jobID, err := strconv.ParseInt(jobIDStr, 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, fmt.Errorf("invalid job ID"))
		return
	}

	db := getDB()
	var job models.AdminScanJob
	err = db.Model(&job).Where("id = ?", jobID).First()
	if err != nil {
		if err == pg.ErrNoRows {
			httputil.NewError(c, http.StatusNotFound, fmt.Errorf("scan job not found"))
			return
		}
		logging.Errorf("Failed to fetch scan job: %v", err)
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, job)
}

// DuplicateGroupsResponse represents the response with duplicate groups
type DuplicateGroupsResponse struct {
	Groups []services.DuplicateGroup `json:"groups"`
	Total  int                       `json:"total"`
}

// GetDuplicateGroups retrieves all duplicate groups
// @Summary Get duplicate groups
// @Description Get all groups of duplicate books
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept json
// @Produce json
// @Success 200 {object} DuplicateGroupsResponse
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/duplicates [get]
func GetDuplicateGroups(c *gin.Context) {
	db := getDB()
	ctx := context.Background()

	groups, err := services.GetDuplicateGroups(ctx, db)
	if err != nil {
		logging.Errorf("Failed to fetch duplicate groups: %v", err)
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, DuplicateGroupsResponse{
		Groups: groups,
		Total:  len(groups),
	})
}

// HideDuplicates hides duplicate books based on the newest ID rule
// @Summary Hide duplicate books
// @Description Hide duplicate books keeping the newest (highest ID)
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept json
// @Produce json
// @Success 200 {object} services.HideResult
// @Failure 403 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/duplicates/hide [post]
func HideDuplicates(c *gin.Context) {
	db := getDB()
	ctx := context.Background()

	result, err := services.HideDuplicates(ctx, db)
	if err != nil {
		logging.Errorf("Failed to hide duplicates: %v", err)
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, result)
}

// getDB returns the database connection
func getDB() *pg.DB {
	// This assumes database.SetDB was called during initialization
	// and there's a way to get it back. We'll use a package-level variable.
	return database.GetDB()
}

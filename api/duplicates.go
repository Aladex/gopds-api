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
	r.POST("/duplicates/scan/:id/stop", StopDuplicateScan)
	r.POST("/duplicates/scan/:id/force-stop", ForceStopDuplicateScan)
	r.GET("/duplicates/scan/active", GetActiveScanJob)
	r.GET("/duplicates/scan/:id", GetScanJobStatus)
	r.GET("/duplicates", GetDuplicateGroups)
	r.POST("/duplicates/hide", HideDuplicates)
}

// ScanJobResponse represents the response when starting a scan
type ScanJobResponse struct {
	JobID int64 `json:"job_id"`
}

type ScanRequest struct {
	Workers int `json:"workers"`
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

	var req ScanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		req.Workers = 1
	}
	if req.Workers < 1 {
		req.Workers = 1
	}
	if req.Workers > 8 {
		req.Workers = 8
	}

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
		ScanParams: fmt.Sprintf(`{"type": "full_scan", "workers": %d}`, req.Workers),
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
	scanCtx, cancel := context.WithCancel(context.Background())
	services.RegisterScanCancel(job.ID, cancel)

	go func() {
		err := services.ScanDuplicates(scanCtx, db, job.ID, wsConn, filesPath, req.Workers)
		if err != nil {
			logging.Errorf("Scan job %d failed: %v", job.ID, err)
		}
		services.UnregisterScanCancel(job.ID)
	}()

	c.JSON(http.StatusOK, ScanJobResponse{
		JobID: job.ID,
	})
}

// StopDuplicateScan requests a running scan to stop
// @Summary Stop duplicate scan
// @Description Stop a running duplicate scan job
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Scan Job ID"
// @Accept json
// @Produce json
// @Success 200 {object} models.Result
// @Failure 404 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/duplicates/scan/{id}/stop [post]
func StopDuplicateScan(c *gin.Context) {
	jobIDStr := c.Param("id")
	jobID, err := strconv.ParseInt(jobIDStr, 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, fmt.Errorf("invalid job ID"))
		return
	}

	if !services.CancelScan(jobID) {
		httputil.NewError(c, http.StatusNotFound, fmt.Errorf("scan job not found or not running"))
		return
	}

	db := getDB()
	now := time.Now()
	_, err = db.Model(&models.AdminScanJob{}).
		Set("status = ?", "failed").
		Set("error = ?", "scan_cancelled").
		Set("finished_at = ?", now).
		Set("updated_at = ?", now).
		Where("id = ?", jobID).
		Update()
	if err != nil {
		logging.Errorf("Failed to mark scan job %d as cancelled: %v", jobID, err)
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, models.Result{Result: "scan_cancelled", Error: nil})
}

// ForceStopDuplicateScan force-stops a scan job by updating DB status
// @Summary Force stop duplicate scan
// @Description Force stop a scan job if context is lost
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Scan Job ID"
// @Accept json
// @Produce json
// @Success 200 {object} models.Result
// @Failure 404 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/duplicates/scan/{id}/force-stop [post]
func ForceStopDuplicateScan(c *gin.Context) {
	jobIDStr := c.Param("id")
	jobID, err := strconv.ParseInt(jobIDStr, 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, fmt.Errorf("invalid job ID"))
		return
	}

	db := getDB()
	var job models.AdminScanJob
	err = db.Model(&job).
		Where("id = ?", jobID).
		First()
	if err != nil {
		if err == pg.ErrNoRows {
			httputil.NewError(c, http.StatusNotFound, fmt.Errorf("scan job not found"))
			return
		}
		logging.Errorf("Failed to fetch scan job %d: %v", jobID, err)
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	if job.Status != "running" && job.Status != "pending" {
		c.JSON(http.StatusOK, models.Result{Result: "no_active_scan", Error: nil})
		return
	}

	now := time.Now()
	_, err = db.Model(&models.AdminScanJob{}).
		Set("status = ?", "failed").
		Set("error = ?", "scan_force_stopped").
		Set("finished_at = ?", now).
		Set("updated_at = ?", now).
		Where("id = ?", jobID).
		Update()
	if err != nil {
		logging.Errorf("Failed to force-stop scan job %d: %v", jobID, err)
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, models.Result{Result: "scan_force_stopped", Error: nil})
}

// GetActiveScanJob retrieves the latest active scan job
// @Summary Get active scan job
// @Description Get the latest running or pending duplicate scan job
// @Tags admin
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept json
// @Produce json
// @Success 200 {object} models.AdminScanJob
// @Failure 404 {object} httputil.HTTPError
// @Failure 500 {object} httputil.HTTPError
// @Router /api/admin/duplicates/scan/active [get]
func GetActiveScanJob(c *gin.Context) {
	db := getDB()
	var job models.AdminScanJob
	err := db.Model(&job).
		WhereIn("status IN (?)", []string{"pending", "running"}).
		Order("id DESC").
		Limit(1).
		Select()
	if err != nil {
		if err == pg.ErrNoRows {
			c.JSON(http.StatusOK, gin.H{"status": "none"})
			return
		}
		logging.Errorf("Failed to fetch active scan job: %v", err)
		httputil.NewError(c, http.StatusInternalServerError, err)
		return
	}

	c.JSON(http.StatusOK, job)
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

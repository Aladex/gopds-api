package api

import (
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"net/http"
	"strconv"
	"time"
)

func GetCollections(c *gin.Context) {
	var filters models.CollectionFilters
	if err := c.ShouldBindQuery(&filters); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	collections, err := database.GetAllPublicCollections(filters)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(200, collections)
}

func GetPrivateCollections(c *gin.Context) {
	userID := c.GetInt64("user_id")

	bookID, _ := strconv.ParseInt(c.Query("book_id"), 10, 64)
	limit, _ := strconv.Atoi(c.Query("limit"))
	offset, _ := strconv.Atoi(c.Query("offset"))

	collections, err := database.GetPrivateCollections(bookID, userID, limit, offset)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(200, collections)
}

func CreateCollection(c *gin.Context) {
	var request struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	userID := c.GetInt64("user_id")
	collection := models.BookCollection{
		UserID:    userID,
		Name:      request.Name,
		IsPublic:  false,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Rating:    0,
	}

	collection, err := database.CreateCollection(collection)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(200, collection)
}

func AddBookToCollection(c *gin.Context) {
	var request struct {
		BookID       int64 `json:"book_id" binding:"required"`
		CollectionID int64 `json:"collection_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	userID := c.GetInt64("user_id")
	err := database.AddBookToCollection(userID, request.CollectionID, request.BookID)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	c.Status(http.StatusOK)
}

func GetBookCollections(c *gin.Context) {
	bookID := c.Param("id")
	userID := c.GetInt64("user_id")

	// Convert bookID to int64
	bookIDInt, err := strconv.ParseInt(bookID, 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	collections, err := database.GetCollectionsByBookID(userID, bookIDInt)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(200, collections)
}

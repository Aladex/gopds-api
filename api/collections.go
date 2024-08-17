package api

import (
	"github.com/gin-gonic/gin"
	"gopds-api/database"
	"gopds-api/httputil"
	"gopds-api/models"
	"gopds-api/tasks"
	"net/http"
	"strconv"
	"time"
)

// UpdateBookPositionRequest struct for updating book position in a collection
type UpdateBookPositionRequest struct {
	BookID       int64 `json:"book_id" binding:"required"`
	CollectionID int64 `json:"collection_id" binding:"required"`
	NewPosition  int   `json:"new_position" binding:"required"`
}

// UpdateCollectionRequest struct for updating a collection
type UpdateCollectionRequest struct {
	Name     string `json:"name" binding:"required"`
	IsPublic *bool  `json:"is_public" binding:"required"`
}

// VoteCollectionRequest struct for voting on a collection
type VoteCollectionRequest struct {
	Vote *bool `json:"vote" binding:"required"`
}

var taskManager *tasks.TaskManager

func SetTaskManager(tm *tasks.TaskManager) {
	taskManager = tm
}

// GetCollections godoc
// @Summary Retrieve the list of collections
// @Description Get a list of collections based on filters
// @Tags collections
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param private query bool false "Filter by private collections"
// @Success 200 {array} models.BookCollection
// @Failure 400 {object} httputil.HTTPError
// @Router /api/books/collections [get]
func GetCollections(c *gin.Context) {
	var filters models.CollectionFilters
	if err := c.ShouldBindQuery(&filters); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	userID := c.GetInt64("user_id")
	isPublic := c.Query("private") != "true"

	collections, err := database.GetCollections(filters, userID, isPublic)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(200, collections)
}

// CreateCollection godoc
// @Summary Create a new collection
// @Description Create a new collection with a specified name
// @Tags collections
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param body body struct { Name string `json:"name" binding:"required"` } true "Collection name"
// @Success 200 {object} models.BookCollection
// @Failure 400 {object} httputil.HTTPError
// @Router /api/books/create-collection [post]
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
	}

	collection, err := database.CreateCollection(collection)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	c.JSON(200, collection)
}

// AddBookToCollection godoc
// @Summary Add a book to a collection
// @Description Add a book to a specified collection
// @Tags collections
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param body body struct { BookID int64 `json:"book_id" binding:"required"`; CollectionID int64 `json:"collection_id" binding:"required"` } true "Book and Collection IDs"
// @Success 200
// @Failure 400 {object} httputil.HTTPError
// @Router /api/books/create-collection [post]
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
	updatedBooks, err := database.AddBookToCollection(userID, request.CollectionID, request.BookID)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	task := tasks.CollectionTask{
		Type:         tasks.UpdateCollection,
		CollectionID: request.CollectionID,
		UpdatedBooks: updatedBooks,
	}

	taskManager.EnqueueTask(task)

	c.Status(http.StatusOK)
}

// RemoveBookFromCollection godoc
// @Summary Remove a book from a collection
// @Description Remove a book from a specified collection
// @Tags collections
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param body body struct { BookID int64 `json:"book_id" binding:"required"`; CollectionID int64 `json:"collection_id" binding:"required"` } true "Book and Collection IDs"
// @Success 200
// @Failure 400 {object} httputil.HTTPError
// @Router /api/books/remove-from-collection [post]
func RemoveBookFromCollection(c *gin.Context) {
	var request struct {
		BookID       int64 `json:"book_id" binding:"required"`
		CollectionID int64 `json:"collection_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	userID := c.GetInt64("user_id")
	updatedBooks, err := database.RemoveBookFromCollection(userID, request.CollectionID, request.BookID)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	task := tasks.CollectionTask{
		Type:         tasks.UpdateCollection,
		CollectionID: request.CollectionID,
		UpdatedBooks: updatedBooks,
	}

	taskManager.EnqueueTask(task)

	c.Status(http.StatusOK)
}

// GetBookCollections godoc
// @Summary Retrieve collections for a book
// @Description Get a list of collections that include a specified book
// @Tags collections
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param id path int true "Book ID"
// @Success 200 {array} models.BookCollection
// @Failure 400 {object} httputil.HTTPError
// @Router /api/books/{id}/collections [get]
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

// UpdateBookPositionInCollection godoc
// @Summary Update the position of a book in a collection
// @Description Update the position of a book within a specified collection
// @Tags collections
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param body body UpdateBookPositionRequest true "Book position update information"
// @Success 200
// @Failure 400 {object} httputil.HTTPError
// @Router /api/books/update-book-position [post]
func UpdateBookPositionInCollection(c *gin.Context) {
	var request UpdateBookPositionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	userID := c.GetInt64("user_id")
	updatedBooks, err := database.UpdateBookPositionInCollection(userID, request.CollectionID, request.BookID, request.NewPosition)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}
	task := tasks.CollectionTask{
		Type:         tasks.UpdateCollection,
		CollectionID: request.CollectionID,
		UpdatedBooks: updatedBooks,
	}

	taskManager.EnqueueTask(task)

	c.Status(http.StatusOK)
}

// UpdateCollection godoc
// @Summary Update a collection
// @Description Update the name and public status of a specified collection
// @Tags collections
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param id path int true "Collection ID"
// @Param body body UpdateCollectionRequest true "Collection update information"
// @Success 200 {object} models.BookCollection
// @Failure 400 {object} httputil.HTTPError
// @Router /api/books/update-collection/{id} [post]
func UpdateCollection(c *gin.Context) {
	var request UpdateCollectionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	collectionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	userID := c.GetInt64("user_id")
	isPublic := false
	if request.IsPublic != nil {
		isPublic = *request.IsPublic
	}

	collection, err := database.UpdateCollection(userID, collectionID, request.Name, isPublic)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, collection)
}

// GetCollection godoc
// @Summary Retrieve collection information
// @Description Get the details of a specified collection
// @Tags collections
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param id path int true "Collection ID"
// @Success 200 {object} models.BookCollection
// @Failure 400 {object} httputil.HTTPError
// @Router /api/collections/{id} [get]
func GetCollection(c *gin.Context) {
	collectionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	collection, err := database.GetCollection(collectionID)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, collection)
}

// VoteCollection godoc
// @Summary Vote on a collection
// @Description Vote on a collection with a plus (true) or minus (false)
// @Tags collections
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Accept  json
// @Produce  json
// @Param id path int true "Collection ID"
// @Param body body VoteCollectionRequest true "Vote information"
// @Success 200 {object} models.BookCollection
// @Failure 400 {object} httputil.HTTPError
// @Router /api/books/vote-collection/{id} [post]
func VoteCollection(c *gin.Context) {
	var request VoteCollectionRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	collectionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	userID := c.GetInt64("user_id")
	userVote := false
	if request.Vote != nil {
		userVote = *request.Vote
	}

	err = database.VoteCollection(userID, collectionID, userVote)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	collection, err := database.GetCollection(collectionID)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	c.JSON(http.StatusOK, collection)
}

// DeleteCollection godoc
// @Summary Delete a collection
// @Description Delete a collection by its ID
// @Tags collections
// @Param Authorization header string true "Token without 'Bearer' prefix"
// @Param id path int true "Collection ID"
// @Success 200
// @Failure 400 {object} httputil.HTTPError
// @Router /api/books/collection/{id} [delete]
func DeleteCollection(c *gin.Context) {
	collectionID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	userID := c.GetInt64("user_id")
	err = database.DeleteCollection(userID, collectionID)
	if err != nil {
		httputil.NewError(c, http.StatusBadRequest, err)
		return
	}

	task := tasks.CollectionTask{
		Type:         tasks.DeleteCollection,
		CollectionID: collectionID,
	}

	taskManager.EnqueueTask(task)

	c.Status(http.StatusOK)
}

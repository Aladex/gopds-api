package telegram

import (
	"context"
	"strconv"
	"testing"

	"gopds-api/models"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubSearch is a no-op services.PublicSearch for manager tests that never
// reach a search flow.
type stubSearch struct{}

func (stubSearch) SearchBooks(context.Context, models.BookSearchRequest) (models.BookSearchPage, error) {
	return models.BookSearchPage{}, nil
}

func (stubSearch) SearchAuthors(context.Context, models.AuthorSearchRequest) (models.AuthorSearchPage, error) {
	return models.AuthorSearchPage{}, nil
}

func (stubSearch) Suggestions(context.Context, models.SuggestionRequest) (models.SuggestionResult, error) {
	return models.SuggestionResult{}, nil
}

func testRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return mr, client
}

func TestNewBotManager(t *testing.T) {
	_, redisClient := testRedis(t)
	config := &Config{BaseURL: "https://example.com"}
	bm := NewBotManager(config, redisClient, stubSearch{})
	assert.NotNil(t, bm)
	assert.NotNil(t, bm.bots)
	assert.Equal(t, config, bm.config)
}

func TestGetBotCount(t *testing.T) {
	_, redisClient := testRedis(t)
	bm := NewBotManager(&Config{BaseURL: "https://example.com"}, redisClient, stubSearch{})
	assert.Equal(t, 0, bm.GetBotCount())

	bm.mutex.Lock()
	bm.bots["test-token-1"] = &Bot{token: "test-token-1", ref: 1}
	bm.mutex.Unlock()
	assert.Equal(t, 1, bm.GetBotCount())

	bm.mutex.Lock()
	bm.bots["test-token-2"] = &Bot{token: "test-token-2", ref: 2}
	bm.mutex.Unlock()
	assert.Equal(t, 2, bm.GetBotCount())
}

func TestListActiveBots(t *testing.T) {
	_, redisClient := testRedis(t)
	bm := NewBotManager(&Config{BaseURL: "https://example.com"}, redisClient, stubSearch{})
	assert.Empty(t, bm.ListActiveBots())

	bm.mutex.Lock()
	bm.bots["1234567890:ABC"] = &Bot{token: "1234567890:ABC", ref: 1}
	bm.bots["0987654321:XYZ"] = &Bot{token: "0987654321:XYZ", ref: 2}
	bm.mutex.Unlock()

	tokens := bm.ListActiveBots()
	assert.Len(t, tokens, 2)
	// Each token is masked and names its bot row.
	for _, token := range tokens {
		assert.Contains(t, token, "***")
		assert.Contains(t, token, "(bot")
	}
}

// S6: what the old interface kept in Redis — conversations, raw reader text,
// and pending input states — goes at start; nothing else does.
func TestTheOldConversationsAreForgotten(t *testing.T) {
	mr, redisClient := testRedis(t)
	for i := 0; i < 1200; i++ {
		require.NoError(t, mr.Set("telegram_conversation:abcdef0123456789:"+strconv.Itoa(i), "raw reader text"))
	}
	require.NoError(t, mr.Set("telegram_user_state:abcdef0123456789:7", "awaiting_author"))
	require.NoError(t, mr.Set("tg2:stranger:1:2", "1"))
	require.NoError(t, mr.Set("session:abc", "keep"))

	bm := NewBotManager(&Config{}, redisClient, stubSearch{})
	bm.forgetConversations(context.Background())
	assert.Equal(t, []string{"session:abc", "tg2:stranger:1:2"}, mr.Keys())

	bm.forgetConversations(context.Background())
	assert.Len(t, mr.Keys(), 2, "a second start finds nothing more")
}

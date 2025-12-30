package sessions

import (
	"net"
	"strconv"
	"testing"

	"gopds-api/config"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis"
	"github.com/stretchr/testify/require"
)

func TestSetRedisConnections(t *testing.T) {
	mainClient := redis.NewClient(&redis.Options{Addr: "localhost:0"})
	tokenClient := redis.NewClient(&redis.Options{Addr: "localhost:0"})
	oldMain, oldToken := rdb, rdbToken

	SetRedisConnections(mainClient, tokenClient)
	t.Cleanup(func() {
		SetRedisConnections(oldMain, oldToken)
		_ = mainClient.Close()
		_ = tokenClient.Close()
	})

	require.Same(t, mainClient, rdb)
	require.Same(t, tokenClient, rdbToken)
}

func TestRedisConnection(t *testing.T) {
	s, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(s.Close)

	port, err := strconv.Atoi(s.Port())
	require.NoError(t, err)

	cfg := &config.Config{
		Redis: config.RedisConfig{
			Host: s.Host(),
			Port: port,
		},
	}

	client := RedisConnection(0, cfg)
	t.Cleanup(func() { _ = client.Close() })

	_, err = client.Ping().Result()
	require.NoError(t, err)
}

func TestRedisConnectionPanicsOnFailure(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)

	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)

	cfg := &config.Config{
		Redis: config.RedisConfig{
			Host: host,
			Port: port,
		},
	}

	require.Panics(t, func() {
		RedisConnection(0, cfg)
	})
}

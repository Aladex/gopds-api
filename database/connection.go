package database

import (
	"context"

	"github.com/go-pg/pg/v10"
)

func SetDB(connection *pg.DB) {
	db = connection
}

func GetDB() *pg.DB {
	return db
}

var db *pg.DB

// DisableJIT turns LLVM JIT compilation off for the connection. The search
// repository's plans are costed high enough to cross jit_above_cost, and a
// ~2 s compile per statement never pays back on millisecond-latency catalogue
// reads. Wire it as pg.Options.OnConnect on every pool that serves search.
func DisableJIT(ctx context.Context, cn *pg.Conn) error {
	_, err := cn.ExecContext(ctx, `SET jit = off`)
	return err
}

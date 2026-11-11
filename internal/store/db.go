// Package store 提供 PostgreSQL 连接与后续的数据访问入口。
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB 包装连接池；URL 为空时表示未配置数据库。
type DB struct {
	pool *pgxpool.Pool
}

// Open 按连接串建立连接池。
//
// @param url PostgreSQL 连接串；为空时返回 nil, nil，调用方需处理未配置的情况
// @param maxConns 连接池上限
// @throws 连接串非法或首次 Ping 失败时返回错误
func Open(ctx context.Context, url string, maxConns int32) (*DB, error) {
	if url == "" {
		return nil, nil
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, err
	}
	return &DB{pool: pool}, nil
}

// Pool 返回底层连接池，供数据访问层使用。
func (d *DB) Pool() *pgxpool.Pool {
	if d == nil {
		return nil
	}
	return d.pool
}

// Ping 检查连接可用性。
func (d *DB) Ping(ctx context.Context) error {
	if d == nil || d.pool == nil {
		return nil
	}
	return d.pool.Ping(ctx)
}

// Close 关闭连接池，重复调用无副作用。
func (d *DB) Close() {
	if d == nil || d.pool == nil {
		return
	}
	d.pool.Close()
}

// Package ha 提供基于 PostgreSQL advisory lock 的单活入口选主。
package ha

import (
	"context"
	"log/slog"
	"time"

	"github.com/moying/agenthub/internal/store"
)

const defaultLockKey int64 = 0x41474855 // "AGHU"

// Leader 让同一数据库上的多个实例只有一个启动飞书长连接。
type Leader struct {
	db  *store.DB
	key int64
	log *slog.Logger
}

// New 构造选主器。
func New(db *store.DB, log *slog.Logger) *Leader {
	if log == nil {
		log = slog.Default()
	}
	return &Leader{db: db, key: defaultLockKey, log: log}
}

// Run 竞争 leader；获得锁时调用 onStart，失去锁/退出时调用 onStop。
func (l *Leader) Run(ctx context.Context, onStart func(context.Context), onStop func()) error {
	if l.db == nil {
		return nil
	}
	for ctx.Err() == nil {
		conn, err := l.db.Pool().Acquire(ctx)
		if err != nil {
			l.log.Warn("ha acquire connection failed", "error", err)
			if !sleep(ctx, 5*time.Second) {
				return ctx.Err()
			}
			continue
		}
		var ok bool
		if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", l.key).Scan(&ok); err != nil || !ok {
			conn.Release()
			if err != nil {
				l.log.Warn("ha try lock failed", "error", err)
			} else {
				l.log.Info("ha standby; another instance holds leader lock")
			}
			if !sleep(ctx, 5*time.Second) {
				return ctx.Err()
			}
			continue
		}

		l.log.Info("ha leader acquired")
		leaderCtx, cancel := context.WithCancel(ctx)
		onStart(leaderCtx)

		ticker := time.NewTicker(5 * time.Second)
		healthy := true
		for healthy {
			select {
			case <-ctx.Done():
				healthy = false
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(context.Background(), 2*time.Second)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					l.log.Warn("ha leader lock lost", "error", err)
					healthy = false
				}
			}
		}
		ticker.Stop()
		cancel()
		if onStop != nil {
			onStop()
		}
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _ = conn.Exec(unlockCtx, "SELECT pg_advisory_unlock($1)", l.key)
		unlockCancel()
		conn.Release()
		l.log.Info("ha leader released")
	}
	return ctx.Err()
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

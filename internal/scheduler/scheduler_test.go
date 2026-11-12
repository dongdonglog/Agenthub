package scheduler_test

import (
	"testing"
	"time"

	"github.com/moying/agenthub/internal/scheduler"
)

func TestNextRun(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	from := time.Date(2026, 11, 12, 8, 30, 0, 0, loc)
	next, err := scheduler.NextRun("0 9 * * *", "Asia/Shanghai", from)
	if err != nil {
		t.Fatal(err)
	}
	if next.Hour() != 9 || next.Minute() != 0 || next.Day() != 12 {
		t.Fatalf("next = %s", next)
	}

	if _, err := scheduler.NextRun("bad expr", "Asia/Shanghai", from); err == nil {
		t.Fatal("非法 cron 应报错")
	}
	if _, err := scheduler.NextRun("0 9 * * *", "No/Zone", from); err == nil {
		t.Fatal("非法时区应报错")
	}
}

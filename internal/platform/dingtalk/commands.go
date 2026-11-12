package dingtalk

import (
	"context"
	"fmt"
	"strings"

	"github.com/moying/agenthub/internal/channel"
)

// handleCommand 处理斜杠命令，返回是否已消费。
func (c *Channel) handleCommand(ctx context.Context, conv, chatID, text string) bool {
	if !strings.HasPrefix(text, "/") {
		return false
	}
	parts := strings.Fields(text)
	cmd := strings.ToLower(parts[0])
	reply := func(msg string) {
		if _, err := c.SendText(ctx, channel.Outbound{ChatID: chatID, Text: msg}); err != nil {
			c.log.Warn("dingtalk command reply failed", "error", err)
		}
	}
	switch cmd {
	case "/status":
		queued, runID := c.engine.Status(conv)
		if runID == "" {
			reply(fmt.Sprintf("排队 %d 条，当前空闲", queued))
		} else {
			reply(fmt.Sprintf("排队 %d 条，运行中：%s", queued, runID))
		}
	case "/model":
		reply(fmt.Sprintf("当前模型：%s\n网关：%s", c.cfg.ModelName, c.cfg.ModelBaseURL))
	case "/stop":
		if c.engine.Stop(conv) {
			reply("已停止当前运行")
		} else {
			reply("当前没有运行中的任务")
		}
	case "/clear":
		n := c.engine.Clear(conv)
		reply(fmt.Sprintf("已取消 %d 条排队消息", n))
	case "/reset":
		c.engine.Reset(conv)
		reply("已停止并开启新会话代")
	case "/help":
		reply("命令：/status /model /stop /clear /reset /help")
	default:
		reply("未知命令，发送 /help 查看")
	}
	return true
}

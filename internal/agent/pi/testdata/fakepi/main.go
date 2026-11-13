// fake pi：测试用的 RPC 替身，按 FAKE_PI_MODE 输出事件。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

func main() {
	mode := os.Getenv("FAKE_PI_MODE")
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		var req map[string]any
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			continue
		}
		if req["type"] != "prompt" {
			continue
		}
		switch mode {
		case "completed":
			emit(map[string]any{"type": "agent_start"})
			emit(map[string]any{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "你好"}})
			emit(map[string]any{"type": "message_end", "message": map[string]any{
				"role": "assistant", "stopReason": "stop",
				"content": []any{map[string]any{"type": "text", "text": "这是 pi 的回答"}},
			}})
			emit(map[string]any{"type": "agent_settled", "aborted": false})
		case "failed":
			emit(map[string]any{"type": "message_end", "message": map[string]any{
				"role": "assistant", "stopReason": "error",
				"content": []any{map[string]any{"type": "text", "text": "部分文本"}},
			}})
			emit(map[string]any{"type": "agent_settled", "aborted": false})
		case "hang":
			time.Sleep(30 * time.Second)
		}
	}
}

func emit(v any) {
	raw, _ := json.Marshal(v)
	fmt.Println(string(raw))
}

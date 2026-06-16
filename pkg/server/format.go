package server

import (
	"encoding/json"
	"fmt"
)

// FormatResponse 将服务器响应格式化为可读字符串，供 CLI 与一键启动模式复用。
// 优先级：错误信息 > 带数据的结果 > 影响行数 > 消息 > 默认成功。
func FormatResponse(resp *Response) string {
	if resp.Code != 0 {
		return fmt.Sprintf("错误: %s", resp.Message)
	}

	if resp.Data != nil {
		data, _ := json.MarshalIndent(resp.Data, "", "  ")
		s := string(data)
		if resp.Rows > 0 {
			return fmt.Sprintf("%d 行:\n%s", resp.Rows, s)
		}
		return s
	}

	if resp.Rows > 0 {
		return fmt.Sprintf("成功，影响 %d 行", resp.Rows)
	}

	if resp.Message != "" {
		return resp.Message
	}

	return "成功"
}

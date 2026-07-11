package tool

import "encoding/json"

// @ 将不同格式的json  统一成一样 忽略字段顺序
func NormalizeJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var value any
	// 先解析 再转成json
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}

	normalized, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(normalized)
}

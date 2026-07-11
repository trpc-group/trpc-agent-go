package tool

import "encoding/json"

func NormalizeJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		// 非法 JSON 仍保留原值，让比较器检测差异。
		return string(raw)
	}

	normalized, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(normalized)
}

package util

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// PathMatcherCallback 路径匹配器的回调函数类型
type PathMatcherCallback func(value interface{}, path []interface{})

// KeyStartCallback key开始时的回调函数类型
// path: 开始的key的完整路径
// 触发时机：当key的第一个值开始解析时
type KeyStartCallback func(path []interface{})

// KeyCompleteCallback key完成时的回调函数类型
// path: 完成的key的完整路径
// finalValue: 该key的最终完整值
// 触发时机：当key的值解析完成时（遇到逗号或对象结束）
type KeyCompleteCallback func(path []interface{}, finalValue interface{})

// PathPattern 路径匹配模式类型
type PathPattern struct {
	Tokens   []interface{} // 解析后的标记数组
	Original string        // 原始模式字符串
	Callback PathMatcherCallback
}

// SimplePathMatcher 简化版 JSON 路径匹配系统
type SimplePathMatcher struct {
	patterns            []PathPattern       // 存储所有注册的模式和回调
	keyStartCallback    KeyStartCallback    // key开始时的回调
	keyCompleteCallback KeyCompleteCallback // key完成时的回调
}

// NewSimplePathMatcher 创建新的路径匹配器
func NewSimplePathMatcher() *SimplePathMatcher {
	return &SimplePathMatcher{
		patterns: make([]PathPattern, 0),
	}
}

// On 注册一个路径模式和对应的回调函数
func (m *SimplePathMatcher) On(pattern string, callback PathMatcherCallback) *SimplePathMatcher {
	// 解析路径模式为标记数组
	parsedPattern := m.parsePath(pattern)
	m.patterns = append(m.patterns, PathPattern{
		Tokens:   parsedPattern,
		Original: pattern,
		Callback: callback,
	})
	return m
}

// OnKeyStart 注册key开始时的回调
// 当一个JSON对象的key开始接收第一个值时，会触发此回调
// 这允许你在key开始时添加开始标记，比如 <start>
func (m *SimplePathMatcher) OnKeyStart(callback KeyStartCallback) {
	m.keyStartCallback = callback
}

// OnKeyComplete 注册key完成时的回调
// 当一个JSON对象的key对应的值解析完成时（遇到逗号或对象结束），会触发此回调
// 这允许你在key完成后进行额外的包装处理，比如添加结束标记 </end>
func (m *SimplePathMatcher) OnKeyComplete(callback KeyCompleteCallback) {
	m.keyCompleteCallback = callback
}

// processPathChar 处理路径解析过程中的单个字符
func (m *SimplePathMatcher) processPathChar(char rune, parts *[]interface{}, currentPart *string, inBrackets *bool) {
	switch char {
	case '.':
		if !*inBrackets {
			if *currentPart != "" {
				*parts = append(*parts, *currentPart)
				*currentPart = ""
			}
		} else {
			*currentPart += string(char)
		}
	case '[':
		if *currentPart != "" {
			*parts = append(*parts, *currentPart)
			*currentPart = ""
		}
		*inBrackets = true
	case ']':
		if *currentPart == "*" {
			*parts = append(*parts, "*")
		} else if num, err := strconv.Atoi(*currentPart); err == nil {
			*parts = append(*parts, num)
		}
		*currentPart = ""
		*inBrackets = false
	default:
		*currentPart += string(char)
	}
}

// parsePath 解析路径字符串为标记数组
func (m *SimplePathMatcher) parsePath(path string) []interface{} {
	if path == "" || path == "$" {
		return []interface{}{"$"}
	}

	// 移除开头的 $ 和 . 符号
	path = strings.TrimPrefix(path, "$")
	path = strings.TrimPrefix(path, ".")

	// 分割路径
	parts := make([]interface{}, 0)
	currentPart := ""
	inBrackets := false

	for _, char := range path {
		m.processPathChar(char, &parts, &currentPart, &inBrackets)
	}

	if currentPart != "" {
		parts = append(parts, currentPart)
	}
	return parts
}

// CheckPatterns 检查当前路径是否匹配任何注册的模式
func (m *SimplePathMatcher) CheckPatterns(path []interface{}, value interface{}) {
	for _, pattern := range m.patterns {
		if m.matchPath(path, pattern.Tokens) {
			// 如果匹配，调用回调函数
			pattern.Callback(value, path)
		}
	}
}

// matchPath 检查路径是否匹配模式
func (m *SimplePathMatcher) matchPath(path []interface{}, pattern []interface{}) bool {
	// 路径长度必须与模式长度完全匹配（精确匹配）
	if len(pattern) != len(path) {
		return false
	}

	// 逐个比较路径元素
	for i := 0; i < len(pattern); i++ {
		patternPart := pattern[i]
		pathPart := path[i]

		// 处理通配符
		if patternPart == "*" {
			continue
		}

		// 处理数组索引
		if patternInt, ok := patternPart.(int); ok {
			if pathInt, ok := pathPart.(int); ok {
				if patternInt != pathInt {
					return false
				}
				continue
			} else {
				return false
			}
		}

		// 处理属性名
		if patternPart != pathPart {
			return false
		}
	}

	return true
}

// ParserState JSON 解析器的状态类型
type ParserState int

// JSON 解析器状态常量定义
const (
	VALUE ParserState = iota
	KEY_OR_END
	KEY
	COLON
	COMMA
	VALUE_OR_END
	NUMBER
	TRUE1
	TRUE2
	TRUE3
	FALSE1
	FALSE2
	FALSE3
	FALSE4
	NULL1
	NULL2
	NULL3
)

// StreamingJsonParser 真实的流式 JSON 解析器
type StreamingJsonParser struct {
	matcher       *SimplePathMatcher
	realtime      bool
	incremental   bool // 新增：控制是返回增量内容还是累积内容
	stack         []interface{}
	path          []interface{}
	state         ParserState
	buffer        string
	isEscaped     bool
	isInString    bool
	currentKey    *string
	arrayIndexes  []int
	lastSentPos   map[string]int         // 新增：记录每个路径上次发送的位置
	keyStarted    map[string]bool        // 新增：记录哪些key已经开始
	pathValues    map[string]interface{} // 新增：记录每个路径的最终值，用于key完成回调
	unicodeBuffer string                 // 新增：用于累积 Unicode 转义序列（\uXXXX）
	unicodeCount  int                    // 新增：记录已累积的 Unicode 十六进制字符数量
}

// NewStreamingJsonParser 创建新的流式JSON解析器
// realtime: 控制是否实时返回解析结果
// incremental: 控制是返回增量内容(true)还是累积内容(false)
func NewStreamingJsonParser(matcher *SimplePathMatcher, realtime bool, incremental bool) *StreamingJsonParser {
	parser := &StreamingJsonParser{
		matcher:     matcher,
		realtime:    realtime,
		incremental: incremental,
	}
	parser.Reset()
	return parser
}

// Reset 重置解析器状态
func (p *StreamingJsonParser) Reset() {
	p.stack = make([]interface{}, 0)
	p.path = make([]interface{}, 0)
	p.state = VALUE
	p.buffer = ""
	p.isEscaped = false
	p.isInString = false
	p.currentKey = nil
	p.arrayIndexes = make([]int, 0)
	p.lastSentPos = make(map[string]int)
	p.keyStarted = make(map[string]bool)
	p.pathValues = make(map[string]interface{})
}

// Write 逐字符处理输入流
func (p *StreamingJsonParser) Write(chunk string) error {
	for _, char := range chunk {
		if err := p.processChar(char); err != nil {
			return err
		}
	}
	return nil
}

// processNonStringChar 处理非字符串状态的字符
func (p *StreamingJsonParser) processNonStringChar(char rune) error {
	switch p.state {
	case VALUE:
		return p.handleValueState(char)
	case KEY_OR_END:
		return p.handleKeyOrEndState(char)
	case KEY:
		return p.handleKeyState(char)
	case COLON:
		return p.handleColonState(char)
	case COMMA:
		return p.handleCommaState(char)
	case VALUE_OR_END:
		return p.handleValueOrEndState(char)
	case NUMBER:
		return p.handleNumberState(char)
	case TRUE1, TRUE2, TRUE3:
		return p.handleTrueState(char)
	case FALSE1, FALSE2, FALSE3, FALSE4:
		return p.handleFalseState(char)
	case NULL1, NULL2, NULL3:
		return p.handleNullState(char)
	}
	return nil
}

// processChar 处理单个字符
func (p *StreamingJsonParser) processChar(char rune) error {
	// 处理字符串中的转义
	if p.isInString {
		return p.handleStringChar(char)
	}

	// 处理非字符串状态
	return p.processNonStringChar(char)
}

// tryTriggerKeyStart 在字符串第一个字符时触发 OnKeyStart
func (p *StreamingJsonParser) tryTriggerKeyStart() {
	if p.state == VALUE && len(p.buffer) == 1 && len(p.path) > 0 {
		pathKey := p.buildPathKey(p.path)
		if !p.keyStarted[pathKey] {
			p.keyStarted[pathKey] = true
			p.notifyKeyStart(p.path)
		}
	}
}

// triggerRealtimeCallback 实时触发回调
func (p *StreamingJsonParser) triggerRealtimeCallback() {
	if !p.realtime || p.state != VALUE || p.buffer == "" {
		return
	}

	if p.incremental {
		// 增量模式：只发送新增的字符
		pathKey := p.getPathKey()
		lastPos := p.lastSentPos[pathKey]
		if len(p.buffer) > lastPos {
			incrementalContent := p.buffer[lastPos:]
			p.matcher.CheckPatterns(p.path, incrementalContent)
			p.lastSentPos[pathKey] = len(p.buffer)
		}
	} else {
		// 累积模式：发送完整内容
		p.matcher.CheckPatterns(p.path, p.buffer)
	}
}

// handleStringChar 处理字符串内的字符
func (p *StreamingJsonParser) handleStringChar(char rune) error {
	// 处理 Unicode 转义序列（\uXXXX）
	if p.unicodeCount > 0 {
		shouldContinue, err := p.handleUnicodeEscape(char)
		if err != nil {
			return err
		}
		if shouldContinue {
			return nil
		}
		// Unicode 处理完成，触发回调
		p.tryTriggerKeyStart()
		p.triggerRealtimeCallback()
		return nil
	}

	// 处理普通转义字符
	if p.isEscaped {
		return p.handleEscapedChar(char)
	}

	// 遇到反斜杠，标记为转义状态
	if char == '\\' {
		p.isEscaped = true
		return nil
	}

	// 遇到引号，字符串结束
	if char == '"' {
		return p.handleStringEnd()
	}

	// 添加普通字符到缓冲区
	p.buffer += string(char)

	// 触发回调
	p.tryTriggerKeyStart()
	p.triggerRealtimeCallback()
	return nil
}

// handleUnicodeEscape 处理 Unicode 转义序列（\uXXXX）
// 返回值：(shouldContinue bool, err error)
// shouldContinue: true 表示还在累积 Unicode 字符，调用者应该直接返回
// false 表示 Unicode 处理完成或失败，调用者应该继续处理（触发实时回调等）
func (p *StreamingJsonParser) handleUnicodeEscape(char rune) (bool, error) {
	// 检查是否是有效的十六进制字符
	if (char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F') {
		p.unicodeBuffer += string(char)
		p.unicodeCount++

		// 累积满 4 个十六进制字符，进行解码（unicodeCount 从 1 开始，所以是 5）
		if p.unicodeCount == 5 {
			var codePoint int64
			_, err := fmt.Sscanf(p.unicodeBuffer, "%x", &codePoint)
			if err == nil {
				p.buffer += string(rune(codePoint))
			} else {
				// 解码失败，保持原样
				p.buffer += "\\u" + p.unicodeBuffer
			}
			// 重置状态
			p.unicodeBuffer = ""
			p.unicodeCount = 0
			p.isEscaped = false
			// 关键修复：返回 true，让调用者直接返回，不要继续处理当前字符
			// 因为当前字符已经作为 Unicode 序列的一部分被处理了
			return true, nil
		}
		// 还在累积中，返回 true
		return true, nil
	}

	// 遇到非十六进制字符，说明不是有效的 Unicode 转义序列
	// 将已累积的内容保持原样输出
	p.buffer += "\\u" + p.unicodeBuffer
	p.unicodeBuffer = ""
	p.unicodeCount = 0
	p.isEscaped = false
	// 返回 false，让调用者继续处理当前字符（因为这个字符不是 Unicode 序列的一部分）
	return false, nil
}

// handleEscapedChar 处理转义字符
func (p *StreamingJsonParser) handleEscapedChar(char rune) error {
	switch char {
	case 'n':
		p.buffer += "\n"
	case 't':
		p.buffer += "\t"
	case 'r':
		p.buffer += "\r"
	case '\\':
		p.buffer += "\\"
	case '"':
		p.buffer += "\""
	case '/':
		p.buffer += "/"
	case 'b':
		p.buffer += "\b"
	case 'f':
		p.buffer += "\f"
	case 'u':
		// 开始 Unicode 转义序列
		p.unicodeCount = 1 // 设置为 1 表示开始累积
		p.unicodeBuffer = ""
		return nil
	default:
		// 对于不认识的转义字符，保持原样
		p.buffer += "\\" + string(char)
	}
	p.isEscaped = false
	return nil
}

// handleStringEnd 处理字符串结束
func (p *StreamingJsonParser) handleStringEnd() error {
	p.isInString = false

	if p.state == KEY {
		// 复制buffer的值而不是引用
		keyValue := p.buffer
		p.currentKey = &keyValue
		p.buffer = ""
		p.state = COLON
	} else if p.state == VALUE {
		// 字符串值完成时，检查是否已经发送过增量内容
		hasIncremental := false
		if p.realtime && p.incremental {
			pathKey := p.getPathKey()
			_, hasIncremental = p.lastSentPos[pathKey]
			delete(p.lastSentPos, pathKey)
		}

		// 🎯 关键修复：无论是否发送过增量内容，都要记录最终值（用于 OnKeyComplete 回调）
		if len(p.path) > 0 {
			pathKey := p.buildPathKey(p.path)
			p.pathValues[pathKey] = p.buffer
		}

		// 只有在非实时增量模式或者没有发送过增量内容时才调用addValue
		if !(p.realtime && p.incremental && hasIncremental) {
			p.addValue(p.buffer)
		}
		p.buffer = ""
		p.state = COMMA
	}

	return nil
}

// handleValueDefault 处理VALUE状态下的数字或空白字符
func (p *StreamingJsonParser) handleValueDefault(char rune) error {
	if char >= '0' && char <= '9' || char == '-' {
		// 开始数字
		p.buffer = string(char)
		p.state = NUMBER
		return nil
	}
	if char != ' ' && char != '\t' && char != '\n' && char != '\r' {
		return fmt.Errorf("unexpected character in VALUE state: %c", char)
	}
	return nil
}

// handleValueState 处理VALUE状态
func (p *StreamingJsonParser) handleValueState(char rune) error {
	switch char {
	case '{':
		// 开始对象
		obj := make(map[string]interface{})
		p.addValue(obj)
		p.stack = append(p.stack, obj)
		p.state = KEY_OR_END
	case '[':
		// 开始数组
		arr := make([]interface{}, 0)
		p.addValue(&arr)
		p.stack = append(p.stack, &arr)
		p.arrayIndexes = append(p.arrayIndexes, 0)
		p.path = append(p.path, 0)
		p.state = VALUE_OR_END
	case '"':
		// 开始字符串
		p.isInString = true
		p.buffer = ""
	case 't':
		// 可能是 true
		p.buffer = "t"
		p.state = TRUE1
	case 'f':
		// 可能是 false
		p.buffer = "f"
		p.state = FALSE1
	case 'n':
		// 可能是 null
		p.buffer = "n"
		p.state = NULL1
	case '-':
		fallthrough
	default:
		return p.handleValueDefault(char)
	}
	return nil
}

// handleKeyOrEndState 处理KEY_OR_END状态
func (p *StreamingJsonParser) handleKeyOrEndState(char rune) error {
	switch char {
	case '}':
		// 结束对象
		p.endObject()
		p.state = COMMA
	case '"':
		// 开始键名
		p.isInString = true
		p.buffer = ""
		p.state = KEY
	default:
		if char != ' ' && char != '\t' && char != '\n' && char != '\r' {
			return fmt.Errorf("unexpected character in KEY_OR_END state: %c", char)
		}
	}
	return nil
}

// handleKeyState 处理KEY状态
func (p *StreamingJsonParser) handleKeyState(char rune) error {
	if char == '"' {
		// 开始字符串
		p.isInString = true
		p.buffer = ""
	} else if char != ' ' && char != '\t' && char != '\n' && char != '\r' {
		return fmt.Errorf("unexpected character in KEY state: %c", char)
	}
	return nil
}

// handleColonState 处理COLON状态
func (p *StreamingJsonParser) handleColonState(char rune) error {
	if char == ':' {
		p.state = VALUE
		// 更新路径 - 添加当前键到路径
		if p.currentKey != nil {
			p.path = append(p.path, *p.currentKey)
		}
	} else if char != ' ' && char != '\t' && char != '\n' && char != '\r' {
		return fmt.Errorf("unexpected character in COLON state: %c", char)
	}
	return nil
}

// handleCommaInContainer 处理容器中的逗号（数组或对象）
func (p *StreamingJsonParser) handleCommaInContainer() {
	if len(p.stack) == 0 {
		return
	}

	if _, isArray := p.stack[len(p.stack)-1].(*[]interface{}); isArray {
		// 数组中的下一个元素
		if len(p.arrayIndexes) > 0 {
			p.arrayIndexes[len(p.arrayIndexes)-1]++
			p.path[len(p.path)-1] = p.arrayIndexes[len(p.arrayIndexes)-1]
		}
		p.state = VALUE
	} else {
		// 对象中的下一个键 - 移除当前键之前，触发 key 完成回调
		if len(p.path) > 0 {
			p.notifyKeyComplete(p.path)
			p.path = p.path[:len(p.path)-1]
		}
		p.state = KEY
	}
}

// handleCommaState 处理COMMA状态
func (p *StreamingJsonParser) handleCommaState(char rune) error {
	switch char {
	case ',':
		p.handleCommaInContainer()
	case '}':
		// 结束对象 - 在结束前触发最后一个 key 的完成回调
		if len(p.path) > 0 {
			p.notifyKeyComplete(p.path)
		}
		p.endObject()
	case ']':
		// 结束数组
		p.endArray()
	default:
		if char != ' ' && char != '\t' && char != '\n' && char != '\r' {
			return fmt.Errorf("unexpected character in COMMA state: %c", char)
		}
	}
	return nil
}

// handleValueOrEndState 处理VALUE_OR_END状态
func (p *StreamingJsonParser) handleValueOrEndState(char rune) error {
	if char == ']' {
		// 空数组
		p.endArray()
		p.state = COMMA
	} else {
		// 回到 VALUE 状态处理这个字符
		p.state = VALUE
		return p.processChar(char)
	}
	return nil
}

// handleNumberState 处理NUMBER状态
func (p *StreamingJsonParser) handleNumberState(char rune) error {
	if (char >= '0' && char <= '9') || char == '.' || char == 'e' || char == 'E' || char == '+' || char == '-' {
		p.buffer += string(char)
	} else {
		// 数字结束
		if num, err := strconv.ParseFloat(p.buffer, 64); err == nil {
			p.addValue(num)
		} else {
			return fmt.Errorf("invalid number: %s", p.buffer)
		}
		p.buffer = ""
		p.state = COMMA
		// 重新处理当前字符
		return p.processChar(char)
	}
	return nil
}

// handleTrueState 处理TRUE状态
func (p *StreamingJsonParser) handleTrueState(char rune) error {
	switch p.state {
	case TRUE1:
		if char == 'r' {
			p.buffer += string(char)
			p.state = TRUE2
		} else {
			return fmt.Errorf("unexpected character in TRUE1 state: %c", char)
		}
	case TRUE2:
		if char == 'u' {
			p.buffer += string(char)
			p.state = TRUE3
		} else {
			return fmt.Errorf("unexpected character in TRUE2 state: %c", char)
		}
	case TRUE3:
		if char == 'e' {
			p.addValue(true)
			p.buffer = ""
			p.state = COMMA
		} else {
			return fmt.Errorf("unexpected character in TRUE3 state: %c", char)
		}
	}
	return nil
}

// handleFalseState 处理FALSE状态
func (p *StreamingJsonParser) handleFalseState(char rune) error {
	switch p.state {
	case FALSE1:
		if char == 'a' {
			p.buffer += string(char)
			p.state = FALSE2
		} else {
			return fmt.Errorf("unexpected character in FALSE1 state: %c", char)
		}
	case FALSE2:
		if char == 'l' {
			p.buffer += string(char)
			p.state = FALSE3
		} else {
			return fmt.Errorf("unexpected character in FALSE2 state: %c", char)
		}
	case FALSE3:
		if char == 's' {
			p.buffer += string(char)
			p.state = FALSE4
		} else {
			return fmt.Errorf("unexpected character in FALSE3 state: %c", char)
		}
	case FALSE4:
		if char == 'e' {
			p.addValue(false)
			p.buffer = ""
			p.state = COMMA
		} else {
			return fmt.Errorf("unexpected character in FALSE4 state: %c", char)
		}
	}
	return nil
}

// handleNullState 处理NULL状态
func (p *StreamingJsonParser) handleNullState(char rune) error {
	switch p.state {
	case NULL1:
		if char == 'u' {
			p.buffer += string(char)
			p.state = NULL2
		} else {
			return fmt.Errorf("unexpected character in NULL1 state: %c", char)
		}
	case NULL2:
		if char == 'l' {
			p.buffer += string(char)
			p.state = NULL3
		} else {
			return fmt.Errorf("unexpected character in NULL2 state: %c", char)
		}
	case NULL3:
		if char == 'l' {
			p.addValue(nil)
			p.buffer = ""
			p.state = COMMA
		} else {
			return fmt.Errorf("unexpected character in NULL3 state: %c", char)
		}
	}
	return nil
}

// addValueToParent 将值添加到父容器（数组或对象）
func (p *StreamingJsonParser) addValueToParent(value interface{}) {
	if len(p.stack) == 0 {
		return
	}

	parent := p.stack[len(p.stack)-1]

	switch container := parent.(type) {
	case *[]interface{}:
		// 添加到数组
		if len(p.arrayIndexes) > 0 {
			index := p.arrayIndexes[len(p.arrayIndexes)-1]
			for len(*container) <= index {
				*container = append(*container, nil)
			}
			(*container)[index] = value
		}
	case map[string]interface{}:
		// 添加到对象
		if p.currentKey != nil {
			container[*p.currentKey] = value
		}
	}
}

// addValue 添加值到当前容器
func (p *StreamingJsonParser) addValue(value interface{}) {
	// 在 key 第一次接收值时，触发 OnKeyStart 回调
	if len(p.path) > 0 {
		pathKey := p.buildPathKey(p.path)
		if !p.keyStarted[pathKey] {
			p.keyStarted[pathKey] = true
			p.notifyKeyStart(p.path)
		}
		p.pathValues[pathKey] = value
	}

	// 根值处理
	if len(p.stack) == 0 {
		p.stack = append(p.stack, value)
		if !(p.realtime && p.incremental && p.hasIncrementalContent(value)) {
			p.matcher.CheckPatterns(p.path, value)
		}
		return
	}

	// 添加到父容器
	p.addValueToParent(value)

	// 触发模式匹配回调
	if !(p.realtime && p.incremental && p.hasIncrementalContent(value)) {
		p.matcher.CheckPatterns(p.path, value)
	}
}

// endObject 结束对象处理
func (p *StreamingJsonParser) endObject() {
	if len(p.stack) > 0 {
		p.stack = p.stack[:len(p.stack)-1]
	}
	// 只有当当前路径的最后一个元素不是数组索引时，才移除路径元素
	// 这样可以保持数组索引在路径中的正确位置
	if len(p.path) > 1 {
		// 检查最后一个路径元素是否为数组索引（整数类型）
		lastElement := p.path[len(p.path)-1]
		if _, isInt := lastElement.(int); !isInt {
			// 如果不是数组索引，则移除路径元素
			p.path = p.path[:len(p.path)-1]
		}
	}
	p.state = COMMA
}

// endArray 结束数组处理
func (p *StreamingJsonParser) endArray() {
	if len(p.stack) > 0 {
		p.stack = p.stack[:len(p.stack)-1]
	}
	if len(p.arrayIndexes) > 0 {
		p.arrayIndexes = p.arrayIndexes[:len(p.arrayIndexes)-1]
	}
	if len(p.path) > 1 {
		p.path = p.path[:len(p.path)-1]
	}
	p.state = COMMA
}

// End 结束解析
func (p *StreamingJsonParser) End() error {
	if len(p.stack) != 1 {
		return errors.New("unexpected end of input: JSON structure is incomplete")
	}
	fmt.Printf("JSON parsing complete: %+v\n", p.stack[0])
	return nil
}

// GetResult 获取解析结果
func (p *StreamingJsonParser) GetResult() interface{} {
	if len(p.stack) > 0 {
		return p.stack[0]
	}
	return nil
}

// getPathKey 生成路径的唯一标识符
func (p *StreamingJsonParser) getPathKey() string {
	var pathStr strings.Builder
	for i, segment := range p.path {
		if i > 0 {
			pathStr.WriteString(".")
		}
		pathStr.WriteString(fmt.Sprintf("%v", segment))
	}
	return pathStr.String()
}

// hasIncrementalContent 检查是否已经发送过增量内容
func (p *StreamingJsonParser) hasIncrementalContent(value interface{}) bool {
	// 只有在实时增量模式下才进行检查
	if !p.realtime || !p.incremental {
		return false
	}

	// 只对字符串类型进行增量处理，检查是否已经发送过增量内容
	if _, isString := value.(string); isString {
		pathKey := p.getPathKey()
		_, exists := p.lastSentPos[pathKey]
		return exists
	}
	// 对于其他类型（数字、布尔值、null、对象、数组），不进行增量处理
	return false
}

// notifyKeyStart 通知 key 开始
func (p *StreamingJsonParser) notifyKeyStart(path []interface{}) {
	if p.matcher.keyStartCallback == nil {
		return
	}

	// 复制路径，避免外部修改
	pathCopy := make([]interface{}, len(path))
	copy(pathCopy, path)

	// 触发回调
	p.matcher.keyStartCallback(pathCopy)
}

// notifyKeyComplete 通知 key 完成
func (p *StreamingJsonParser) notifyKeyComplete(path []interface{}) {
	if p.matcher.keyCompleteCallback == nil {
		return
	}

	// 构建路径字符串作为 key
	pathKey := p.buildPathKey(path)

	// 获取该路径的最终值
	finalValue := p.pathValues[pathKey]

	// 复制路径，避免外部修改
	pathCopy := make([]interface{}, len(path))
	copy(pathCopy, path)

	// 触发回调
	p.matcher.keyCompleteCallback(pathCopy, finalValue)
}

// buildPathKey 构建路径的字符串 key
func (p *StreamingJsonParser) buildPathKey(path []interface{}) string {
	parts := make([]string, len(path))
	for i, part := range path {
		parts[i] = fmt.Sprintf("%v", part)
	}
	return strings.Join(parts, ".")
}

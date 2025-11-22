package text

import (
	"fmt"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/text"
)

// Match compares source and target using the configured strategy.
func Match(t *text.TextCriterion, source, target string) error {
	if t.Compare != nil {
		return t.Compare(source, target)
	}
	if t.Ignore {
		return nil
	}
	if t.CaseInsensitive {
		source = strings.ToLower(source)
		target = strings.ToLower(target)
	}
	switch t.MatchStrategy {
	case text.TextMatchStrategyExact:
		if source == target {
			return nil
		}
		return fmt.Errorf("source %s and target %s do not match", source, target)
	case text.TextMatchStrategyContains:
		if strings.Contains(source, target) {
			return nil
		}
		return fmt.Errorf("source %s does not contain target %s", source, target)
	case text.TextMatchStrategyRegex:
		re, err := regexp.Compile(target)
		if err != nil {
			return fmt.Errorf("invalid regex %s: %w", target, err)
		}
		if re.MatchString(source) {
			return nil
		}
		return fmt.Errorf("source %s does not match regex %s", source, target)
	default:
		return fmt.Errorf("invalid match strategy %s", t.MatchStrategy)
	}
}

package query

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

type SelectorKind uint8

const (
	SelectorName SelectorKind = iota
	SelectorIndex
	SelectorWildcard
	SelectorSlice
	SelectorDescendantName
	SelectorDescendantWildcard
	SelectorFilter
)

type Selector struct {
	Kind      SelectorKind
	Name      string
	Index     int64
	Start     int64
	End       int64
	HasEnd    bool
	Step      int64
	Filter    string
	predicate filterPredicate
}

type JSONPathPlan struct {
	expression string
	selectors  []Selector
}

const maxJSONPathSelectors = 64

func CompileJSONPath(expression string) (*JSONPathPlan, *core.AppError) {
	if expression == "" || expression[0] != '$' {
		return nil, jsonPathSyntaxError("a JSONPath expression must begin with '$'")
	}
	plan := &JSONPathPlan{expression: expression}
	for position := 1; position < len(expression); {
		switch expression[position] {
		case '.':
			descendant := position+1 < len(expression) && expression[position+1] == '.'
			if descendant {
				position += 2
			} else {
				position++
			}
			if position >= len(expression) || expression[position] == '.' {
				return nil, jsonPathSyntaxError("expected a name or wildcard after '.'")
			}
			if expression[position] == '*' {
				kind := SelectorWildcard
				if descendant {
					kind = SelectorDescendantWildcard
				}
				plan.selectors = append(plan.selectors, Selector{Kind: kind})
				position++
				continue
			}
			name, next := readShorthandName(expression, position)
			if name == "" {
				return nil, jsonPathSyntaxError("invalid shorthand member name")
			}
			kind := SelectorName
			if descendant {
				kind = SelectorDescendantName
			}
			plan.selectors = append(plan.selectors, Selector{Kind: kind, Name: name})
			position = next
		case '[':
			content, next, err := readBracketContent(expression, position)
			if err != nil {
				return nil, err
			}
			selector, selectorErr := parseBracketSelector(strings.TrimSpace(content))
			if selectorErr != nil {
				return nil, selectorErr
			}
			plan.selectors = append(plan.selectors, selector)
			position = next
		default:
			return nil, jsonPathSyntaxError("expected '.' or '[' after a JSONPath segment")
		}
	}
	if len(plan.selectors) > maxJSONPathSelectors {
		return nil, unsupportedJSONPath("the streaming JSONPath profile supports at most 64 selectors")
	}
	descendants := 0
	hasFilter := false
	for index, selector := range plan.selectors {
		switch selector.Kind {
		case SelectorDescendantName, SelectorDescendantWildcard:
			descendants++
			if index != len(plan.selectors)-1 {
				return nil, unsupportedJSONPath("selectors after a descendant require non-streaming segment ordering")
			}
		case SelectorFilter:
			hasFilter = true
		}
	}
	if descendants > 1 {
		return nil, unsupportedJSONPath("multiple descendant selectors can duplicate or reorder results outside the streaming profile")
	}
	if descendants > 0 && hasFilter {
		return nil, unsupportedJSONPath("combining descendant and filter selectors requires non-streaming result processing")
	}
	return plan, nil
}

func (plan *JSONPathPlan) Matches(path core.Path) bool {
	return matchSelectors(plan.selectors, 0, path, 0)
}

func (plan *JSONPathPlan) Expression() string { return plan.expression }

func (plan *JSONPathPlan) HasFilter() bool {
	for _, selector := range plan.selectors {
		if selector.Kind == SelectorFilter {
			return true
		}
	}
	return false
}

func (plan *JSONPathPlan) firstFilterIndex() int {
	for index, selector := range plan.selectors {
		if selector.Kind == SelectorFilter {
			return index
		}
	}
	return -1
}

func (plan *JSONPathPlan) IsFilterCandidate(path core.Path) bool {
	filterIndex := plan.firstFilterIndex()
	if filterIndex < 0 || len(path) == 0 {
		return false
	}
	parent := path[:len(path)-1]
	return matchSelectors(plan.selectors[:filterIndex], 0, parent, 0)
}

func (plan *JSONPathPlan) Selectors() []Selector {
	result := make([]Selector, len(plan.selectors))
	copy(result, plan.selectors)
	return result
}

func matchSelectors(selectors []Selector, selectorIndex int, path core.Path, pathIndex int) bool {
	if selectorIndex == len(selectors) {
		return pathIndex == len(path)
	}
	selector := selectors[selectorIndex]
	if selector.Kind == SelectorFilter {
		return false
	}
	if selector.Kind == SelectorDescendantName || selector.Kind == SelectorDescendantWildcard {
		for candidate := pathIndex; candidate < len(path); candidate++ {
			if selectorMatchesSegment(selector, path[candidate]) && matchSelectors(selectors, selectorIndex+1, path, candidate+1) {
				return true
			}
		}
		return false
	}
	if pathIndex >= len(path) || !selectorMatchesSegment(selector, path[pathIndex]) {
		return false
	}
	return matchSelectors(selectors, selectorIndex+1, path, pathIndex+1)
}

func selectorMatchesSegment(selector Selector, segment core.PathSegment) bool {
	switch selector.Kind {
	case SelectorName, SelectorDescendantName:
		return segment.Kind == core.SegmentProperty && segment.Name == selector.Name
	case SelectorIndex:
		return segment.Kind == core.SegmentIndex && segment.Index == selector.Index
	case SelectorWildcard, SelectorDescendantWildcard:
		return true
	case SelectorSlice:
		if segment.Kind != core.SegmentIndex || segment.Index < selector.Start {
			return false
		}
		if selector.HasEnd && segment.Index >= selector.End {
			return false
		}
		return (segment.Index-selector.Start)%selector.Step == 0
	default:
		return false
	}
}

func readShorthandName(expression string, start int) (string, int) {
	position := start
	for position < len(expression) {
		value := expression[position]
		if value == '.' || value == '[' {
			break
		}
		if value == ']' || value == '*' || value <= ' ' {
			return "", start
		}
		position++
	}
	if position == start {
		return "", start
	}
	name := expression[start:position]
	if !validShorthandName(name) {
		return "", start
	}
	return name, position
}

func validShorthandName(name string) bool {
	if !utf8.ValidString(name) {
		return false
	}
	for index, value := range []rune(name) {
		if value == '_' || value >= 0x80 || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || index > 0 && value >= '0' && value <= '9' {
			continue
		}
		return false
	}
	return true
}

func readBracketContent(expression string, start int) (string, int, *core.AppError) {
	depth := 1
	quote := byte(0)
	escaped := false
	for position := start + 1; position < len(expression); position++ {
		value := expression[position]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if value == '\\' {
				escaped = true
			} else if value == quote {
				quote = 0
			}
			continue
		}
		if value == '\'' || value == '"' {
			quote = value
			continue
		}
		switch value {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return expression[start+1 : position], position + 1, nil
			}
		}
	}
	return "", 0, jsonPathSyntaxError("unterminated bracket selector")
}

func parseBracketSelector(content string) (Selector, *core.AppError) {
	if content == "" {
		return Selector{}, jsonPathSyntaxError("empty bracket selector")
	}
	if content == "*" {
		return Selector{Kind: SelectorWildcard}, nil
	}
	if containsUnquotedByte(content, ',') {
		return Selector{}, unsupportedJSONPath("multi-selector unions can reorder or duplicate results")
	}
	if content[0] == '\'' || content[0] == '"' {
		name, err := parseJSONPathString(content)
		if err != nil {
			return Selector{}, err
		}
		return Selector{Kind: SelectorName, Name: name}, nil
	}
	if content[0] == '?' {
		filter := strings.TrimSpace(content[1:])
		if filter == "" {
			return Selector{}, jsonPathSyntaxError("empty filter selector")
		}
		predicate, err := compileFilter(filter)
		if err != nil {
			return Selector{}, err
		}
		return Selector{Kind: SelectorFilter, Filter: filter, predicate: predicate}, nil
	}
	if strings.Contains(content, ":") {
		return parseSliceSelector(content)
	}
	if strings.HasPrefix(content, "-") {
		return Selector{}, unsupportedJSONPath("negative array indexes require array-length buffering")
	}
	if !canonicalNonNegativeInteger(content) {
		return Selector{}, jsonPathSyntaxError("invalid array index")
	}
	index, err := strconv.ParseInt(content, 10, 64)
	if err != nil || index < 0 {
		return Selector{}, jsonPathSyntaxError("invalid array index")
	}
	return Selector{Kind: SelectorIndex, Index: index}, nil
}

func containsUnquotedByte(content string, target byte) bool {
	var quote byte
	escaped := false
	for index := 0; index < len(content); index++ {
		value := content[index]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if value == '\\' {
				escaped = true
			} else if value == quote {
				quote = 0
			}
			continue
		}
		if value == '\'' || value == '"' {
			quote = value
			continue
		}
		if value == target {
			return true
		}
	}
	return false
}

func parseSliceSelector(content string) (Selector, *core.AppError) {
	parts := strings.Split(content, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return Selector{}, jsonPathSyntaxError("a slice must contain two or three fields")
	}
	values := []int64{0, 0, 1}
	present := []bool{false, false, false}
	for index, raw := range parts {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.HasPrefix(raw, "-") {
			return Selector{}, unsupportedJSONPath("negative and reverse slices require array-length buffering")
		}
		if !canonicalNonNegativeInteger(raw) {
			return Selector{}, jsonPathSyntaxError("invalid slice field")
		}
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return Selector{}, jsonPathSyntaxError("invalid slice field")
		}
		values[index] = value
		present[index] = true
	}
	if values[2] <= 0 {
		return Selector{}, unsupportedJSONPath("zero or reverse slice steps are outside the streaming profile")
	}
	return Selector{Kind: SelectorSlice, Start: values[0], End: values[1], HasEnd: present[1], Step: values[2]}, nil
}

func canonicalNonNegativeInteger(raw string) bool {
	if raw == "0" {
		return true
	}
	if raw == "" || raw[0] < '1' || raw[0] > '9' {
		return false
	}
	for index := 1; index < len(raw); index++ {
		if raw[index] < '0' || raw[index] > '9' {
			return false
		}
	}
	return true
}

func parseJSONPathString(content string) (string, *core.AppError) {
	quote := content[0]
	if len(content) < 2 || content[len(content)-1] != quote {
		return "", jsonPathSyntaxError("unterminated quoted member name")
	}
	if quote == '"' {
		var decoded string
		if err := json.Unmarshal([]byte(content), &decoded); err != nil {
			return "", jsonPathSyntaxError("invalid quoted member name")
		}
		return decoded, nil
	}

	var transformed strings.Builder
	transformed.WriteByte('"')
	for position := 1; position < len(content)-1; position++ {
		value := content[position]
		if value == '\\' {
			if position+1 >= len(content)-1 {
				return "", jsonPathSyntaxError("invalid escape in quoted member name")
			}
			next := content[position+1]
			if next == '\'' {
				transformed.WriteByte('\'')
				position++
				continue
			}
			transformed.WriteByte('\\')
			transformed.WriteByte(next)
			position++
			continue
		}
		if value == '"' {
			transformed.WriteString(`\"`)
		} else if value == '\'' {
			return "", jsonPathSyntaxError("unescaped quote in member name")
		} else {
			transformed.WriteByte(value)
		}
	}
	transformed.WriteByte('"')
	var decoded string
	if err := json.Unmarshal([]byte(transformed.String()), &decoded); err != nil {
		return "", jsonPathSyntaxError("invalid quoted member name")
	}
	return decoded, nil
}

func jsonPathSyntaxError(message string) *core.AppError {
	return &core.AppError{Code: core.CodeQuerySyntax, Message: message}
}

func unsupportedJSONPath(message string) *core.AppError {
	return &core.AppError{Code: core.CodeUnsupportedQueryFeature, Message: message}
}

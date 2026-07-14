package query

import (
	"encoding/json"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

type filterPredicate interface {
	evaluate(candidate any, checkpoint Checkpoint) (bool, *core.AppError)
}

type Checkpoint func() *core.AppError

const (
	maxFilterPredicateNodes = 128
	maxFilterPathSegments   = 64
	maxFilterNesting        = 64
)

func runCheckpoint(checkpoint Checkpoint) *core.AppError {
	if checkpoint == nil {
		return nil
	}
	return checkpoint()
}

type filterOperand interface {
	resolve(candidate any) (value any, exists bool)
	isPath() bool
}

type logicalPredicate struct {
	operator string
	left     filterPredicate
	right    filterPredicate
}

func (predicate logicalPredicate) evaluate(candidate any, checkpoint Checkpoint) (bool, *core.AppError) {
	if err := runCheckpoint(checkpoint); err != nil {
		return false, err
	}
	left, err := predicate.left.evaluate(candidate, checkpoint)
	if err != nil {
		return false, err
	}
	if predicate.operator == "&&" && !left {
		return false, nil
	}
	if predicate.operator == "||" && left {
		return true, nil
	}
	return predicate.right.evaluate(candidate, checkpoint)
}

type notPredicate struct{ inner filterPredicate }

func (predicate notPredicate) evaluate(candidate any, checkpoint Checkpoint) (bool, *core.AppError) {
	if err := runCheckpoint(checkpoint); err != nil {
		return false, err
	}
	value, err := predicate.inner.evaluate(candidate, checkpoint)
	return !value, err
}

type existencePredicate struct{ operand filterOperand }

func (predicate existencePredicate) evaluate(candidate any, checkpoint Checkpoint) (bool, *core.AppError) {
	if err := runCheckpoint(checkpoint); err != nil {
		return false, err
	}
	_, exists := predicate.operand.resolve(candidate)
	return exists, nil
}

type comparisonPredicate struct {
	operator string
	left     filterOperand
	right    filterOperand
}

func (predicate comparisonPredicate) evaluate(candidate any, checkpoint Checkpoint) (bool, *core.AppError) {
	if err := runCheckpoint(checkpoint); err != nil {
		return false, err
	}
	left, leftExists := predicate.left.resolve(candidate)
	right, rightExists := predicate.right.resolve(candidate)
	if !leftExists || !rightExists {
		return false, nil
	}
	comparison, comparable, err := compareScalars(left, right)
	if err != nil {
		return false, err
	}
	if isOrderingOperator(predicate.operator) && !orderableScalarPair(left, right) {
		if predicate.operator == "<=" || predicate.operator == ">=" {
			return comparable && comparison == 0, nil
		}
		return false, nil
	}
	switch predicate.operator {
	case "==":
		return comparable && comparison == 0, nil
	case "!=":
		return !comparable || comparison != 0, nil
	case "<":
		return comparable && comparison < 0, nil
	case "<=":
		return comparable && comparison <= 0, nil
	case ">":
		return comparable && comparison > 0, nil
	case ">=":
		return comparable && comparison >= 0, nil
	default:
		return false, &core.AppError{Code: core.CodeInternal, Message: "unknown filter comparison operator"}
	}
}

func isOrderingOperator(operator string) bool {
	return operator == "<" || operator == "<=" || operator == ">" || operator == ">="
}

func orderableScalarPair(left, right any) bool {
	_, leftNumber := left.(json.Number)
	_, rightNumber := right.(json.Number)
	if leftNumber || rightNumber {
		return leftNumber && rightNumber
	}
	_, leftString := left.(string)
	_, rightString := right.(string)
	return leftString && rightString
}

type literalOperand struct{ value any }

func (operand literalOperand) resolve(any) (any, bool) { return operand.value, true }
func (literalOperand) isPath() bool                    { return false }

type pathOperand struct{ segments core.Path }

func (operand pathOperand) resolve(candidate any) (any, bool) {
	current := candidate
	for _, segment := range operand.segments {
		if segment.Kind == core.SegmentProperty {
			object, ok := current.(map[string]any)
			if !ok {
				return nil, false
			}
			current, ok = object[segment.Name]
			if !ok {
				return nil, false
			}
			continue
		}
		array, ok := current.([]any)
		if !ok || segment.Index < 0 || segment.Index >= int64(len(array)) {
			return nil, false
		}
		current = array[segment.Index]
	}
	return current, true
}

func (pathOperand) isPath() bool { return true }

type filterParser struct {
	input          string
	pos            int
	predicateNodes int
	nesting        int
}

func compileFilter(input string) (filterPredicate, *core.AppError) {
	parser := &filterParser{input: input}
	predicate, err := parser.parseOr()
	if err != nil {
		return nil, err
	}
	parser.skipSpace()
	if parser.pos != len(parser.input) {
		return nil, jsonPathSyntaxError("unexpected token in filter expression")
	}
	return predicate, nil
}

func (parser *filterParser) parseOr() (filterPredicate, *core.AppError) {
	left, err := parser.parseAnd()
	if err != nil {
		return nil, err
	}
	for parser.consume("||") {
		right, rightErr := parser.parseAnd()
		if rightErr != nil {
			return nil, rightErr
		}
		if nodeErr := parser.addPredicateNode(); nodeErr != nil {
			return nil, nodeErr
		}
		left = logicalPredicate{operator: "||", left: left, right: right}
	}
	return left, nil
}

func (parser *filterParser) parseAnd() (filterPredicate, *core.AppError) {
	left, err := parser.parseUnary()
	if err != nil {
		return nil, err
	}
	for parser.consume("&&") {
		right, rightErr := parser.parseUnary()
		if rightErr != nil {
			return nil, rightErr
		}
		if nodeErr := parser.addPredicateNode(); nodeErr != nil {
			return nil, nodeErr
		}
		left = logicalPredicate{operator: "&&", left: left, right: right}
	}
	return left, nil
}

func (parser *filterParser) parseUnary() (filterPredicate, *core.AppError) {
	parser.skipSpace()
	if parser.has("!") && !parser.has("!=") {
		if nodeErr := parser.addPredicateNode(); nodeErr != nil {
			return nil, nodeErr
		}
		parser.pos++
		inner, err := parser.parseUnary()
		if err != nil {
			return nil, err
		}
		return notPredicate{inner: inner}, nil
	}
	if parser.consume("(") {
		parser.nesting++
		if parser.nesting > maxFilterNesting {
			return nil, unsupportedJSONPath("filter expressions support at most 64 nested groups")
		}
		inner, err := parser.parseOr()
		if err != nil {
			parser.nesting--
			return nil, err
		}
		if !parser.consume(")") {
			parser.nesting--
			return nil, jsonPathSyntaxError("unterminated parenthesized filter expression")
		}
		parser.nesting--
		return inner, nil
	}
	return parser.parseTest()
}

func (parser *filterParser) parseTest() (filterPredicate, *core.AppError) {
	left, err := parser.parseOperand()
	if err != nil {
		return nil, err
	}
	operator := parser.comparisonOperator()
	if operator == "" {
		if !left.isPath() {
			return nil, jsonPathSyntaxError("a filter literal must be part of a comparison")
		}
		if nodeErr := parser.addPredicateNode(); nodeErr != nil {
			return nil, nodeErr
		}
		return existencePredicate{operand: left}, nil
	}
	right, rightErr := parser.parseOperand()
	if rightErr != nil {
		return nil, rightErr
	}
	if nodeErr := parser.addPredicateNode(); nodeErr != nil {
		return nil, nodeErr
	}
	return comparisonPredicate{operator: operator, left: left, right: right}, nil
}

func (parser *filterParser) addPredicateNode() *core.AppError {
	parser.predicateNodes++
	if parser.predicateNodes > maxFilterPredicateNodes {
		return unsupportedJSONPath("filter expressions support at most 128 predicate nodes")
	}
	return nil
}

func (parser *filterParser) parseOperand() (filterOperand, *core.AppError) {
	parser.skipSpace()
	if parser.pos >= len(parser.input) {
		return nil, jsonPathSyntaxError("missing filter operand")
	}
	if parser.input[parser.pos] == '@' {
		return parser.parsePathOperand()
	}
	if parser.input[parser.pos] == '$' {
		return nil, unsupportedJSONPath("root references are outside the streaming JSONPath profile")
	}
	if parser.input[parser.pos] == '\'' || parser.input[parser.pos] == '"' {
		literal, next, err := readFilterString(parser.input, parser.pos)
		if err != nil {
			return nil, err
		}
		parser.pos = next
		return literalOperand{value: literal}, nil
	}
	for _, keyword := range []struct {
		name  string
		value any
	}{{"true", true}, {"false", false}, {"null", nil}} {
		if parser.consumeKeyword(keyword.name) {
			return literalOperand{value: keyword.value}, nil
		}
	}
	if parser.input[parser.pos] == '-' || parser.input[parser.pos] >= '0' && parser.input[parser.pos] <= '9' {
		start := parser.pos
		for parser.pos < len(parser.input) && isFilterNumberByte(parser.input[parser.pos]) {
			parser.pos++
		}
		raw := parser.input[start:parser.pos]
		if !json.Valid([]byte(raw)) {
			return nil, jsonPathSyntaxError("invalid JSON number in filter")
		}
		return literalOperand{value: json.Number(raw)}, nil
	}
	identifier, _ := readFilterIdentifier(parser.input, parser.pos)
	if identifier != "" {
		return nil, unsupportedJSONPath("JSONPath functions are outside the streaming profile")
	}
	return nil, jsonPathSyntaxError("invalid filter operand")
}

func (parser *filterParser) parsePathOperand() (filterOperand, *core.AppError) {
	parser.pos++
	var segments core.Path
	for parser.pos < len(parser.input) {
		if parser.input[parser.pos] == '.' {
			parser.pos++
			name, next := readFilterIdentifier(parser.input, parser.pos)
			if name == "" {
				return nil, jsonPathSyntaxError("expected a member name after '@.'")
			}
			segments = append(segments, core.PropertySegment(name))
			if len(segments) > maxFilterPathSegments {
				return nil, unsupportedJSONPath("filter paths support at most 64 segments")
			}
			parser.pos = next
			continue
		}
		if parser.input[parser.pos] == '[' {
			content, next, err := readBracketContent(parser.input, parser.pos)
			if err != nil {
				return nil, err
			}
			content = strings.TrimSpace(content)
			if content != "" && (content[0] == '\'' || content[0] == '"') {
				name, nameErr := parseJSONPathString(content)
				if nameErr != nil {
					return nil, nameErr
				}
				segments = append(segments, core.PropertySegment(name))
			} else {
				if strings.HasPrefix(content, "-") {
					return nil, unsupportedJSONPath("negative filter indexes require array-length buffering")
				}
				if !canonicalNonNegativeInteger(content) {
					return nil, jsonPathSyntaxError("invalid filter array index")
				}
				index, indexErr := strconv.ParseInt(content, 10, 64)
				if indexErr != nil || index < 0 {
					return nil, jsonPathSyntaxError("invalid filter array index")
				}
				segments = append(segments, core.IndexSegment(index))
			}
			if len(segments) > maxFilterPathSegments {
				return nil, unsupportedJSONPath("filter paths support at most 64 segments")
			}
			parser.pos = next
			continue
		}
		break
	}
	return pathOperand{segments: segments}, nil
}

func (parser *filterParser) comparisonOperator() string {
	parser.skipSpace()
	for _, operator := range []string{"==", "!=", "<=", ">=", "<", ">"} {
		if strings.HasPrefix(parser.input[parser.pos:], operator) {
			parser.pos += len(operator)
			return operator
		}
	}
	return ""
}

func (parser *filterParser) skipSpace() {
	for parser.pos < len(parser.input) {
		switch parser.input[parser.pos] {
		case ' ', '\t', '\r', '\n':
			parser.pos++
		default:
			return
		}
	}
}

func (parser *filterParser) has(token string) bool {
	parser.skipSpace()
	return strings.HasPrefix(parser.input[parser.pos:], token)
}

func (parser *filterParser) consume(token string) bool {
	if !parser.has(token) {
		return false
	}
	parser.pos += len(token)
	return true
}

func (parser *filterParser) consumeKeyword(keyword string) bool {
	parser.skipSpace()
	if !strings.HasPrefix(parser.input[parser.pos:], keyword) {
		return false
	}
	end := parser.pos + len(keyword)
	if end < len(parser.input) && isFilterNameByte(parser.input[end]) {
		return false
	}
	parser.pos = end
	return true
}

func readFilterIdentifier(input string, start int) (string, int) {
	if start >= len(input) || !isFilterNameStart(input[start]) {
		return "", start
	}
	position := start + 1
	for position < len(input) && isFilterNameByte(input[position]) {
		position++
	}
	return input[start:position], position
}

func isFilterNameStart(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= utf8.RuneSelf
}

func isFilterNameByte(value byte) bool {
	return isFilterNameStart(value) || value >= '0' && value <= '9'
}

func isFilterNumberByte(value byte) bool {
	return value >= '0' && value <= '9' || value == '+' || value == '-' || value == '.' || value == 'e' || value == 'E'
}

func readFilterString(input string, start int) (string, int, *core.AppError) {
	quote := input[start]
	escaped := false
	for position := start + 1; position < len(input); position++ {
		if escaped {
			escaped = false
			continue
		}
		if input[position] == '\\' {
			escaped = true
			continue
		}
		if input[position] == quote {
			value, err := parseJSONPathString(input[start : position+1])
			return value, position + 1, err
		}
	}
	return "", 0, jsonPathSyntaxError("unterminated string literal in filter")
}

func compareScalars(left, right any) (int, bool, *core.AppError) {
	leftNumber, leftIsNumber := left.(json.Number)
	rightNumber, rightIsNumber := right.(json.Number)
	if leftIsNumber || rightIsNumber {
		if !leftIsNumber || !rightIsNumber {
			return 0, false, nil
		}
		return compareJSONNumbers(leftNumber, rightNumber), true, nil
	}
	switch typedLeft := left.(type) {
	case nil:
		if right == nil {
			return 0, true, nil
		}
		return 0, false, nil
	case string:
		typedRight, ok := right.(string)
		if !ok {
			return 0, false, nil
		}
		return strings.Compare(typedLeft, typedRight), true, nil
	case bool:
		typedRight, ok := right.(bool)
		if !ok {
			return 0, false, nil
		}
		if typedLeft == typedRight {
			return 0, true, nil
		}
		if !typedLeft {
			return -1, true, nil
		}
		return 1, true, nil
	default:
		return 0, false, &core.AppError{Code: core.CodeUnsupportedQueryFeature, Message: "deep container comparison is outside the streaming JSONPath profile"}
	}
}

type decimalNumber struct {
	sign   int
	digits string
	point  *big.Int
}

func compareJSONNumbers(left, right json.Number) int {
	leftDecimal := parseDecimalNumber(string(left))
	rightDecimal := parseDecimalNumber(string(right))
	if leftDecimal.sign != rightDecimal.sign {
		if leftDecimal.sign < rightDecimal.sign {
			return -1
		}
		return 1
	}
	if leftDecimal.sign == 0 {
		return 0
	}
	comparison := leftDecimal.point.Cmp(rightDecimal.point)
	if comparison == 0 {
		width := len(leftDecimal.digits)
		if len(rightDecimal.digits) > width {
			width = len(rightDecimal.digits)
		}
		for index := 0; index < width; index++ {
			leftDigit, rightDigit := byte('0'), byte('0')
			if index < len(leftDecimal.digits) {
				leftDigit = leftDecimal.digits[index]
			}
			if index < len(rightDecimal.digits) {
				rightDigit = rightDecimal.digits[index]
			}
			if leftDigit < rightDigit {
				comparison = -1
				break
			}
			if leftDigit > rightDigit {
				comparison = 1
				break
			}
		}
	}
	if leftDecimal.sign < 0 {
		return -comparison
	}
	return comparison
}

func parseDecimalNumber(raw string) decimalNumber {
	sign := 1
	if strings.HasPrefix(raw, "-") {
		sign = -1
		raw = raw[1:]
	}
	exponent := new(big.Int)
	if marker := strings.IndexAny(raw, "eE"); marker >= 0 {
		exponent.SetString(raw[marker+1:], 10)
		raw = raw[:marker]
	}
	dot := strings.IndexByte(raw, '.')
	integerDigits := len(raw)
	if dot >= 0 {
		integerDigits = dot
		raw = raw[:dot] + raw[dot+1:]
	}
	leading := len(raw) - len(strings.TrimLeft(raw, "0"))
	raw = strings.TrimLeft(raw, "0")
	if raw == "" {
		return decimalNumber{point: new(big.Int)}
	}
	point := new(big.Int).SetInt64(int64(integerDigits - leading))
	point.Add(point, exponent)
	return decimalNumber{sign: sign, digits: raw, point: point}
}

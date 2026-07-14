package query

import (
	"github.com/oovz/mcp-json-reader/v2/internal/core"
)

type Plan struct {
	language   core.QueryLanguage
	expression string
	pointer    *PointerPlan
	jsonPath   *JSONPathPlan
}

func Compile(language core.QueryLanguage, expression string) (*Plan, *core.AppError) {
	switch language {
	case core.QueryPointer:
		pointer, err := CompilePointer(expression)
		if err != nil {
			return nil, err
		}
		return &Plan{language: language, expression: expression, pointer: pointer}, nil
	case core.QueryJSONPath:
		jsonPath, err := CompileJSONPath(expression)
		if err != nil {
			return nil, err
		}
		return &Plan{language: language, expression: expression, jsonPath: jsonPath}, nil
	default:
		return nil, &core.AppError{Code: core.CodeQuerySyntax, Message: "unsupported query language"}
	}
}

func (plan *Plan) Language() core.QueryLanguage { return plan.language }
func (plan *Plan) Expression() string           { return plan.expression }

func (plan *Plan) Matches(path core.Path) bool {
	if plan.pointer != nil {
		return plan.pointer.Matches(path)
	}
	return plan.jsonPath.Matches(path)
}

func (plan *Plan) HasFilter() bool {
	return plan.jsonPath != nil && plan.jsonPath.HasFilter()
}

func (plan *Plan) IsFilterCandidate(path core.Path) bool {
	return plan.jsonPath != nil && plan.jsonPath.IsFilterCandidate(path)
}

func (plan *Plan) VisitFilterCandidate(value any, path core.Path, checkpoint Checkpoint, visitor func(DOMMatch) bool) *core.AppError {
	if plan.jsonPath == nil {
		return nil
	}
	return plan.jsonPath.VisitFilterCandidate(value, path, checkpoint, visitor)
}

package query

import (
	"sort"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
)

type DOMMatch struct {
	Path  core.Path
	Value any
}

// VisitFilterCandidate evaluates the first filter against a bounded candidate
// and walks any remaining selectors without materializing an intermediate
// nodelist. Returning false from visitor stops the walk immediately.
func (plan *JSONPathPlan) VisitFilterCandidate(value any, path core.Path, checkpoint Checkpoint, visitor func(DOMMatch) bool) *core.AppError {
	if err := runCheckpoint(checkpoint); err != nil {
		return err
	}
	filterIndex := plan.firstFilterIndex()
	if filterIndex < 0 {
		return nil
	}
	selected, err := plan.selectors[filterIndex].predicate.evaluate(value, checkpoint)
	if err != nil || !selected {
		return err
	}
	_, visitErr := visitDOMSelectors(
		DOMMatch{Path: append(core.Path(nil), path...), Value: value},
		plan.selectors[filterIndex+1:],
		0,
		checkpoint,
		visitor,
	)
	return visitErr
}

func visitDOMSelectors(node DOMMatch, selectors []Selector, index int, checkpoint Checkpoint, visitor func(DOMMatch) bool) (bool, *core.AppError) {
	if err := runCheckpoint(checkpoint); err != nil {
		return false, err
	}
	if index == len(selectors) {
		return visitor(node), nil
	}
	selector := selectors[index]
	visit := func(child DOMMatch) (bool, *core.AppError) {
		return visitDOMSelectors(child, selectors, index+1, checkpoint, visitor)
	}
	switch selector.Kind {
	case SelectorName:
		object, ok := node.Value.(map[string]any)
		if !ok {
			return true, nil
		}
		value, exists := object[selector.Name]
		if !exists {
			return true, nil
		}
		return visit(DOMMatch{Path: node.Path.Append(core.PropertySegment(selector.Name)), Value: value})
	case SelectorIndex:
		array, ok := node.Value.([]any)
		if !ok || selector.Index < 0 || selector.Index >= int64(len(array)) {
			return true, nil
		}
		return visit(DOMMatch{Path: node.Path.Append(core.IndexSegment(selector.Index)), Value: array[selector.Index]})
	case SelectorWildcard:
		return visitDOMChildren(node, checkpoint, visit)
	case SelectorSlice:
		array, ok := node.Value.([]any)
		if !ok {
			return true, nil
		}
		end := int64(len(array))
		if selector.HasEnd && selector.End < end {
			end = selector.End
		}
		for childIndex := selector.Start; childIndex < end; {
			keepGoing, childErr := visit(DOMMatch{Path: node.Path.Append(core.IndexSegment(childIndex)), Value: array[childIndex]})
			if childErr != nil || !keepGoing {
				return keepGoing, childErr
			}
			// Compare before adding so even an internal oversized step cannot wrap.
			if selector.Step >= end-childIndex {
				break
			}
			childIndex += selector.Step
		}
		return true, nil
	case SelectorFilter:
		return visitDOMChildren(node, checkpoint, func(child DOMMatch) (bool, *core.AppError) {
			selected, selectErr := selector.predicate.evaluate(child.Value, checkpoint)
			if selectErr != nil || !selected {
				return selectErr == nil, selectErr
			}
			return visit(child)
		})
	default:
		return false, &core.AppError{Code: core.CodeInternal, Message: "unknown JSONPath selector"}
	}
}

func visitDOMChildren(node DOMMatch, checkpoint Checkpoint, visitor func(DOMMatch) (bool, *core.AppError)) (bool, *core.AppError) {
	if err := runCheckpoint(checkpoint); err != nil {
		return false, err
	}
	switch value := node.Value.(type) {
	case []any:
		for index, child := range value {
			if err := runCheckpoint(checkpoint); err != nil {
				return false, err
			}
			keepGoing, err := visitor(DOMMatch{Path: node.Path.Append(core.IndexSegment(int64(index))), Value: child})
			if err != nil || !keepGoing {
				return keepGoing, err
			}
		}
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := runCheckpoint(checkpoint); err != nil {
				return false, err
			}
			keepGoing, err := visitor(DOMMatch{Path: node.Path.Append(core.PropertySegment(key)), Value: value[key]})
			if err != nil || !keepGoing {
				return keepGoing, err
			}
		}
	}
	return true, nil
}

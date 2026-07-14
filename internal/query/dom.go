package query

import (
	"sort"

	"github.com/oovz/mcp-json-reader/v2/internal/core"
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
		for childIndex := selector.Start; childIndex < end; childIndex += selector.Step {
			keepGoing, childErr := visit(DOMMatch{Path: node.Path.Append(core.IndexSegment(childIndex)), Value: array[childIndex]})
			if childErr != nil || !keepGoing {
				return keepGoing, childErr
			}
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
	case SelectorDescendantName, SelectorDescendantWildcard:
		return false, unsupportedJSONPath("descendant selectors cannot follow filters in the streaming profile")
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

func (plan *JSONPathPlan) SelectDOM(root any, base core.Path) ([]DOMMatch, *core.AppError) {
	nodes := []DOMMatch{{Path: append(core.Path(nil), base...), Value: root}}
	for _, selector := range plan.selectors {
		var next []DOMMatch
		for _, node := range nodes {
			selected, err := selectDOMChildren(node, selector)
			if err != nil {
				return nil, err
			}
			next = append(next, selected...)
		}
		nodes = next
	}
	return nodes, nil
}

func selectDOMChildren(node DOMMatch, selector Selector) ([]DOMMatch, *core.AppError) {
	switch selector.Kind {
	case SelectorName:
		object, ok := node.Value.(map[string]any)
		if !ok {
			return nil, nil
		}
		value, exists := object[selector.Name]
		if !exists {
			return nil, nil
		}
		return []DOMMatch{{Path: node.Path.Append(core.PropertySegment(selector.Name)), Value: value}}, nil
	case SelectorIndex:
		array, ok := node.Value.([]any)
		if !ok || selector.Index < 0 || selector.Index >= int64(len(array)) {
			return nil, nil
		}
		return []DOMMatch{{Path: node.Path.Append(core.IndexSegment(selector.Index)), Value: array[selector.Index]}}, nil
	case SelectorWildcard:
		return allDOMChildren(node), nil
	case SelectorSlice:
		array, ok := node.Value.([]any)
		if !ok {
			return nil, nil
		}
		end := int64(len(array))
		if selector.HasEnd && selector.End < end {
			end = selector.End
		}
		var matches []DOMMatch
		for index := selector.Start; index < end; index += selector.Step {
			matches = append(matches, DOMMatch{Path: node.Path.Append(core.IndexSegment(index)), Value: array[index]})
		}
		return matches, nil
	case SelectorDescendantName, SelectorDescendantWildcard:
		var matches []DOMMatch
		collectDescendants(node, selector, &matches)
		return matches, nil
	case SelectorFilter:
		children := allDOMChildren(node)
		var matches []DOMMatch
		for _, child := range children {
			selected, err := selector.predicate.evaluate(child.Value, nil)
			if err != nil {
				return nil, err
			}
			if selected {
				matches = append(matches, child)
			}
		}
		return matches, nil
	default:
		return nil, &core.AppError{Code: core.CodeInternal, Message: "unknown JSONPath selector"}
	}
}

func allDOMChildren(node DOMMatch) []DOMMatch {
	switch value := node.Value.(type) {
	case []any:
		matches := make([]DOMMatch, 0, len(value))
		for index, child := range value {
			matches = append(matches, DOMMatch{Path: node.Path.Append(core.IndexSegment(int64(index))), Value: child})
		}
		return matches
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		matches := make([]DOMMatch, 0, len(keys))
		for _, key := range keys {
			matches = append(matches, DOMMatch{Path: node.Path.Append(core.PropertySegment(key)), Value: value[key]})
		}
		return matches
	default:
		return nil
	}
}

func collectDescendants(node DOMMatch, selector Selector, matches *[]DOMMatch) {
	for _, child := range allDOMChildren(node) {
		if selector.Kind == SelectorDescendantWildcard || child.Path[len(child.Path)-1].Kind == core.SegmentProperty && child.Path[len(child.Path)-1].Name == selector.Name {
			*matches = append(*matches, child)
		}
		collectDescendants(child, selector, matches)
	}
}

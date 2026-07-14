package core

import (
	"strconv"
	"strings"
)

type SegmentKind uint8

const (
	SegmentProperty SegmentKind = iota
	SegmentIndex
)

type PathSegment struct {
	Kind  SegmentKind
	Name  string
	Index int64
}

func PropertySegment(name string) PathSegment {
	return PathSegment{Kind: SegmentProperty, Name: name}
}

func IndexSegment(index int64) PathSegment {
	return PathSegment{Kind: SegmentIndex, Index: index}
}

type Path []PathSegment

func (path Path) Pointer() string {
	if len(path) == 0 {
		return ""
	}
	var result strings.Builder
	for _, segment := range path {
		result.WriteByte('/')
		if segment.Kind == SegmentIndex {
			result.WriteString(strconv.FormatInt(segment.Index, 10))
			continue
		}
		escaped := strings.ReplaceAll(segment.Name, "~", "~0")
		escaped = strings.ReplaceAll(escaped, "/", "~1")
		result.WriteString(escaped)
	}
	return result.String()
}

func (path Path) Append(segment PathSegment) Path {
	result := make(Path, len(path)+1)
	copy(result, path)
	result[len(path)] = segment
	return result
}

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
		for index := 0; index < len(segment.Name); index++ {
			switch segment.Name[index] {
			case '~':
				result.WriteString("~0")
			case '/':
				result.WriteString("~1")
			default:
				result.WriteByte(segment.Name[index])
			}
		}
	}
	return result.String()
}

func (path Path) Append(segment PathSegment) Path {
	result := make(Path, len(path)+1)
	copy(result, path)
	result[len(path)] = segment
	return result
}

// PointerJSONSize includes both RFC 6901 and JSON string escaping, without
// constructing the potentially large pointer. Call it before retaining paths.
func (path Path) PointerJSONSize() int64 {
	size := int64(2)
	for _, segment := range path {
		size++ // slash separating path segments
		if segment.Kind == SegmentIndex {
			size += JSONIntegerSize(segment.Index)
			continue
		}
		size += JSONStringSize(segment.Name) - 2
		size += int64(strings.Count(segment.Name, "~") + strings.Count(segment.Name, "/"))
	}
	return size
}

// DiagnosticPointer omits a path that would enlarge an error beyond its
// diagnostic budget. Byte/record coordinates remain available.
func (path Path) DiagnosticPointer() string {
	if path.PointerJSONSize() > 1024 {
		return ""
	}
	return path.Pointer()
}

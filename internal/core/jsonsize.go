package core

import (
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"
)

var ErrJSONSize = errors.New("encoded JSON exceeds the byte budget")

// JSONStringSize matches encoding/json's default HTML/JavaScript-safe encoding
// without allocating an escaped copy. Quotes are included.
func JSONStringSize(value string) int64 {
	size := int64(2)
	for index := 0; index < len(value); {
		character := value[index]
		if character < utf8.RuneSelf {
			switch character {
			case '"', '\\', '\b', '\f', '\n', '\r', '\t':
				size += 2
			case '<', '>', '&':
				size += 6
			default:
				if character < 0x20 {
					size += 6
				} else {
					size++
				}
			}
			index++
			continue
		}
		r, width := utf8.DecodeRuneInString(value[index:])
		if r == utf8.RuneError && width == 1 {
			size += 3 // Go 1.27 writes U+FFFD as UTF-8.
		} else if r == '\u2028' || r == '\u2029' {
			size += 6
		} else {
			size += int64(width)
		}
		index += width
	}
	return size
}

func JSONIntegerSize(value int64) int64 {
	var buffer [20]byte
	return int64(len(strconv.AppendInt(buffer[:0], value, 10)))
}

// JSONSize measures the validated JSON value domain used by filter candidates.
// It stops at the budget before Marshal can allocate a full encoded value.
// It deliberately accepts only JSON decoder values, not arbitrary Go structs.
func JSONSize(value any, limit int64) (int64, error) {
	var size int64
	add := func(count int64) error {
		if count > limit-size {
			return ErrJSONSize
		}
		size += count
		return nil
	}
	var visit func(any) error
	visit = func(value any) error {
		switch value := value.(type) {
		case nil:
			return add(4)
		case bool:
			if value {
				return add(4)
			}
			return add(5)
		case string:
			return add(JSONStringSize(value))
		case json.Number:
			return add(int64(len(value)))
		case []any:
			if value == nil {
				return add(4)
			}
			if err := add(2); err != nil {
				return err
			}
			for index, element := range value {
				if index > 0 {
					if err := add(1); err != nil {
						return err
					}
				}
				if err := visit(element); err != nil {
					return err
				}
			}
		case map[string]any:
			if value == nil {
				return add(4)
			}
			if err := add(2); err != nil {
				return err
			}
			index := 0
			for name, element := range value {
				comma := int64(0)
				if index > 0 {
					comma = 1
				}
				if err := add(JSONStringSize(name) + 1 + comma); err != nil {
					return err
				}
				if err := visit(element); err != nil {
					return err
				}
				index++
			}
		default:
			return errors.New("unsupported internal JSON value type")
		}
		return nil
	}
	if limit < 0 {
		return 0, ErrJSONSize
	}
	err := visit(value)
	return size, err
}

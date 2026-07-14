package core

// Format describes how standard JSON values are framed in a source file.
type Format string

const (
	FormatAuto         Format = "auto"
	FormatJSON         Format = "json"
	FormatJSONL        Format = "jsonl"
	FormatJSONSequence Format = "json-seq"
)

// ParseFormat validates a tool-facing format name.
func ParseFormat(name string) (Format, *AppError) {
	format := Format(name)
	switch format {
	case FormatAuto, FormatJSON, FormatJSONL, FormatJSONSequence:
		return format, nil
	}
	return "", &AppError{
		Code:    CodeUnsupportedFormat,
		Message: "unsupported JSON file format: " + name,
	}
}

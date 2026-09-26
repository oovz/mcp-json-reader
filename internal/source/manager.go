package source

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/oovz/mcp-json-reader/v3/internal/core"
	"github.com/oovz/mcp-json-reader/v3/internal/stream"
)

type ManagerOptions struct {
	Now func() time.Time
}

type OpenOptions struct {
	Path       string
	Format     core.Format
	Validation core.ValidationMode
	Ephemeral  bool
}

type ValidationInfo struct {
	Mode            core.ValidationMode `json:"mode"`
	Complete        bool                `json:"complete"`
	BytesExamined   int64               `json:"bytes_examined"`
	RecordsExamined int64               `json:"records_examined,omitempty"`
}

type SourceInfo struct {
	Size         int64  `json:"size"`
	ModifiedTime string `json:"modified_time"`
}

type OpenInfo struct {
	FileID          string         `json:"file_id"`
	RequestedFormat core.Format    `json:"requested_format"`
	DetectedFormat  core.Format    `json:"detected_format"`
	Detection       *Detection     `json:"detection,omitempty"`
	Validation      ValidationInfo `json:"validation"`
	Source          SourceInfo     `json:"source"`
}

type fingerprint struct {
	size       int64
	modifiedNS int64
}

func fingerprintOf(info os.FileInfo) fingerprint {
	return fingerprint{size: info.Size(), modifiedNS: info.ModTime().UnixNano()}
}

type handle struct {
	mu sync.Mutex

	id          string
	relPath     string
	file        *os.File
	format      core.Format
	ephemeral   bool
	original    os.FileInfo
	fingerprint fingerprint
	expires     time.Time
	closed      bool
}

type Manager struct {
	mu sync.Mutex

	root     *os.Root
	rootPath string
	limits   core.Limits
	now      func() time.Time
	handles  map[string]*handle
	closed   bool
}

func NewManager(rootPath string, limits core.Limits, options ManagerOptions) (*Manager, error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(rootPath)
	if err != nil {
		return nil, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, err
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Manager{root: root, rootPath: filepath.Clean(absolute), limits: limits, now: now, handles: make(map[string]*handle)}, nil
}

func (manager *Manager) Open(ctx context.Context, options OpenOptions) (OpenInfo, *core.AppError) {
	ctx, cancel := context.WithTimeout(ctx, manager.limits.MaxScanTime)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return OpenInfo{}, core.ContextError(err, manager.limits.MaxScanTime)
	}
	requestedFormat := options.Format
	if requestedFormat == "" {
		requestedFormat = core.FormatAuto
	}
	if _, err := core.ParseFormat(string(requestedFormat)); err != nil {
		return OpenInfo{}, err
	}
	validation := options.Validation
	if validation == "" {
		validation = core.ValidationProbe
	}
	if _, err := core.ParseValidationMode(string(validation)); err != nil {
		return OpenInfo{}, err
	}

	relPath, pathErr := manager.relativePath(options.Path)
	if pathErr != nil {
		return OpenInfo{}, pathErr
	}
	manager.cleanupExpired()
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return OpenInfo{}, &core.AppError{Code: core.CodeInternal, Message: "source manager is closed"}
	}
	if count := len(manager.handles); count >= manager.limits.MaxOpenFiles {
		manager.mu.Unlock()
		return OpenInfo{}, &core.AppError{Code: core.CodeResourceLimit, Message: "max_open_files exceeded", Limit: &core.LimitDetail{Name: "max_open_files", Limit: int64(manager.limits.MaxOpenFiles), Value: int64(count + 1)}}
	}
	manager.mu.Unlock()

	file, openErr := openConfined(manager.root, relPath)
	if openErr != nil {
		return OpenInfo{}, manager.openError(openErr)
	}
	if err := ctx.Err(); err != nil {
		_ = file.Close()
		return OpenInfo{}, core.ContextError(err, manager.limits.MaxScanTime)
	}
	keepFile := false
	defer func() {
		if !keepFile {
			_ = file.Close()
		}
	}()
	info, statErr := file.Stat()
	if statErr != nil {
		return OpenInfo{}, &core.AppError{Code: core.CodeIO, Message: "cannot inspect requested source"}
	}
	if !info.Mode().IsRegular() {
		return OpenInfo{}, &core.AppError{Code: core.CodeInvalidArgument, Message: "requested source is not a regular file"}
	}

	prefix, prefixErr := readPrefix(file, info.Size(), manager.limits.ProbeBytes)
	if prefixErr != nil {
		return OpenInfo{}, prefixErr
	}
	selectedFormat := requestedFormat
	var detection *Detection
	if selectedFormat == core.FormatAuto {
		detected, details := DetectFormat(relPath, prefix, manager.limits.ProbeRecords)
		selectedFormat = detected
		detection = &details
	}
	validationInfo, validateErr := manager.validateOpen(ctx, file, info.Size(), prefix, selectedFormat, validation)
	if validateErr != nil {
		return OpenInfo{}, validateErr
	}
	if _, seekErr := file.Seek(0, io.SeekStart); seekErr != nil {
		return OpenInfo{}, &core.AppError{Code: core.CodeIO, Message: "cannot rewind requested source"}
	}
	finalInfo, finalStatErr := file.Stat()
	pathInfo, pathStatErr := manager.root.Stat(relPath)
	if finalStatErr != nil || pathStatErr != nil || !os.SameFile(info, pathInfo) || fingerprintOf(finalInfo) != fingerprintOf(info) || fingerprintOf(pathInfo) != fingerprintOf(info) {
		return OpenInfo{}, &core.AppError{Code: core.CodeSourceChanged, Message: "source changed while it was being opened"}
	}
	if err := ctx.Err(); err != nil {
		return OpenInfo{}, core.ContextError(err, manager.limits.MaxScanTime)
	}
	id, idErr := newID("jf_")
	if idErr != nil {
		return OpenInfo{}, &core.AppError{Code: core.CodeInternal, Message: "cannot allocate a file handle"}
	}
	opened := &handle{
		id:          id,
		relPath:     relPath,
		file:        file,
		format:      selectedFormat,
		ephemeral:   options.Ephemeral,
		original:    info,
		fingerprint: fingerprintOf(info),
		expires:     manager.now().Add(manager.limits.HandleTTL),
	}
	manager.mu.Lock()
	if manager.closed || len(manager.handles) >= manager.limits.MaxOpenFiles {
		manager.mu.Unlock()
		return OpenInfo{}, &core.AppError{Code: core.CodeResourceLimit, Message: "max_open_files exceeded", Limit: &core.LimitDetail{Name: "max_open_files", Limit: int64(manager.limits.MaxOpenFiles)}}
	}
	manager.handles[id] = opened
	manager.mu.Unlock()
	keepFile = true
	return OpenInfo{
		FileID:          id,
		RequestedFormat: requestedFormat,
		DetectedFormat:  selectedFormat,
		Detection:       detection,
		Validation:      validationInfo,
		Source:          SourceInfo{Size: info.Size(), ModifiedTime: info.ModTime().UTC().Format(time.RFC3339Nano)},
	}, nil
}

func (manager *Manager) validateOpen(ctx context.Context, file *os.File, size int64, prefix []byte, format core.Format, mode core.ValidationMode) (ValidationInfo, *core.AppError) {
	if mode == core.ValidationProbe && size <= manager.limits.ProbeBytes && format != core.FormatJSON {
		end, records := 0, 0
		switch format {
		case core.FormatJSONL:
			end, records = completeJSONLLines(prefix, manager.limits.ProbeRecords)
		case core.FormatJSONSequence:
			end, records = completeJSONSequenceRecords(prefix, manager.limits.ProbeRecords)
			// Separator runs contain no records. Include a trailing run in a
			// complete probe, without consuming any later record.
			if records == manager.limits.ProbeRecords && len(bytes.Trim(prefix[end:], "\x1e")) == 0 {
				end = len(prefix)
			}
		}
		if records < manager.limits.ProbeRecords {
			end = len(prefix)
		}
		summary, err := stream.ValidateSource(ctx, bytes.NewReader(prefix[:end]), format, manager.limits)
		if err != nil {
			return ValidationInfo{}, err
		}
		return ValidationInfo{Mode: mode, Complete: int64(end) == size, BytesExamined: int64(end), RecordsExamined: summary.Records}, nil
	}
	if mode == core.ValidationFull || size <= manager.limits.ProbeBytes {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return ValidationInfo{}, &core.AppError{Code: core.CodeIO, Message: "cannot seek requested source"}
		}
		summary, validateErr := stream.ValidateSource(ctx, file, format, manager.limits)
		if validateErr != nil {
			return ValidationInfo{}, validateErr
		}
		return ValidationInfo{Mode: mode, Complete: true, BytesExamined: size, RecordsExamined: summary.Records}, nil
	}

	records, bytesExamined, probeErr := validateLargeProbe(ctx, prefix, format, manager.limits)
	if probeErr != nil {
		return ValidationInfo{}, probeErr
	}
	return ValidationInfo{Mode: mode, Complete: false, BytesExamined: bytesExamined, RecordsExamined: records}, nil
}

func validateLargeProbe(ctx context.Context, prefix []byte, format core.Format, limits core.Limits) (int64, int64, *core.AppError) {
	probe := validUTF8ProbePrefix(prefix)
	switch format {
	case core.FormatJSON:
		if len(prefix) > 0 && prefix[0] == 0x1e {
			return 0, 0, &core.AppError{Code: core.CodeFormatMismatch, Message: "expected one JSON document, but found a JSON sequence record separator", ExpectedFormat: core.FormatJSON, LikelyFormats: []core.Format{core.FormatJSONSequence}, Retry: &core.Retry{Format: core.FormatJSONSequence}, Location: &core.Location{ByteOffset: core.Int64(0)}}
		}
		if _, err := stream.ValidateSource(ctx, bytes.NewReader(probe), format, limits); err != nil && !incompleteProbeError(err) {
			return 0, int64(len(probe)), err
		}
		return 0, int64(len(probe)), nil
	case core.FormatJSONL:
		end, records := completeJSONLLines(probe, limits.ProbeRecords)
		validationEnd := len(probe)
		if records == limits.ProbeRecords {
			validationEnd = end
		}
		trailingRecordIncomplete := validationEnd == len(probe) && end < validationEnd
		if _, err := stream.ValidateSource(ctx, bytes.NewReader(probe[:validationEnd]), format, limits); err != nil && !incompleteFramedProbeError(err, int64(records), trailingRecordIncomplete) {
			return 0, int64(validationEnd), err
		}
		return int64(records), int64(validationEnd), nil
	case core.FormatJSONSequence:
		if len(prefix) == 0 || prefix[0] != 0x1e {
			return 0, 0, &core.AppError{Code: core.CodeFormatMismatch, Message: "expected a JSON sequence record separator at byte 0", ExpectedFormat: core.FormatJSONSequence, LikelyFormats: []core.Format{core.FormatJSON, core.FormatJSONL}, Location: &core.Location{ByteOffset: core.Int64(0)}}
		}
		end, records := completeJSONSequenceRecords(probe, limits.ProbeRecords)
		validationEnd := len(probe)
		if records == limits.ProbeRecords {
			validationEnd = end
		}
		trailingRecordIncomplete := validationEnd == len(probe) && end < validationEnd
		if _, err := stream.ValidateSource(ctx, bytes.NewReader(probe[:validationEnd]), format, limits); err != nil && !incompleteFramedProbeError(err, int64(records), trailingRecordIncomplete) {
			return 0, int64(validationEnd), err
		}
		return int64(records), int64(validationEnd), nil
	default:
		return 0, 0, &core.AppError{Code: core.CodeInvalidArgument, Message: "probe requires an explicit format"}
	}
}

func validUTF8ProbePrefix(prefix []byte) []byte {
	if utf8.Valid(prefix) {
		return prefix
	}
	minimum := len(prefix) - 3
	if minimum < 0 {
		minimum = 0
	}
	for end := len(prefix) - 1; end >= minimum; end-- {
		if utf8.Valid(prefix[:end]) {
			if incompleteUTF8Prefix(prefix[end:]) {
				return prefix[:end]
			}
			return prefix
		}
	}
	// The invalid encoding is not confined to a possibly truncated final rune;
	// leave it intact so the lexical guard reports it.
	return prefix
}

func incompleteUTF8Prefix(suffix []byte) bool {
	if len(suffix) == 0 || len(suffix) > 3 {
		return false
	}
	first := suffix[0]
	width := 0
	switch {
	case first >= 0xc2 && first <= 0xdf:
		width = 2
	case first >= 0xe0 && first <= 0xef:
		width = 3
	case first >= 0xf0 && first <= 0xf4:
		width = 4
	default:
		return false
	}
	if len(suffix) >= width {
		return false
	}
	for _, value := range suffix[1:] {
		if value < 0x80 || value > 0xbf {
			return false
		}
	}
	if len(suffix) > 1 {
		second := suffix[1]
		switch first {
		case 0xe0:
			if second < 0xa0 {
				return false
			}
		case 0xed:
			if second > 0x9f {
				return false
			}
		case 0xf0:
			if second < 0x90 {
				return false
			}
		case 0xf4:
			if second > 0x8f {
				return false
			}
		}
	}
	return true
}

func incompleteProbeError(err *core.AppError) bool {
	if err == nil || err.Code != core.CodeSyntax {
		return false
	}
	return errors.Is(err, io.ErrUnexpectedEOF)
}

func incompleteFramedProbeError(err *core.AppError, completeRecords int64, trailingRecordIncomplete bool) bool {
	if !trailingRecordIncomplete || !incompleteProbeError(err) || err.Location == nil || err.Location.RecordIndex == nil {
		return false
	}
	return *err.Location.RecordIndex == completeRecords
}

func completeJSONLLines(prefix []byte, maximum int) (int, int) {
	end, records, offset := 0, 0, 0
	for records < maximum {
		newline := bytes.IndexByte(prefix[offset:], '\n')
		if newline < 0 {
			break
		}
		offset += newline + 1
		end = offset
		records++
	}
	return end, records
}

func completeJSONSequenceRecords(prefix []byte, maximum int) (int, int) {
	end, records := 0, 0
	inRecord := false
	for offset, value := range prefix {
		if value == 0x1e {
			if inRecord {
				records++
				inRecord = false
				end = offset
				if records == maximum {
					return end, records
				}
			}
		} else {
			inRecord = true
		}
	}
	return end, records
}

func readPrefix(file *os.File, size, limit int64) ([]byte, *core.AppError) {
	length := size
	if length > limit {
		length = limit
	}
	buffer := make([]byte, int(length))
	count, err := file.ReadAt(buffer, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, &core.AppError{Code: core.CodeIO, Message: "cannot inspect requested source"}
	}
	return buffer[:count], nil
}

func (manager *Manager) relativePath(requested string) (string, *core.AppError) {
	if strings.TrimSpace(requested) == "" {
		return "", &core.AppError{Code: core.CodeInvalidArgument, Message: "path is required"}
	}
	path := filepath.FromSlash(requested)
	var relative string
	if filepath.IsAbs(path) {
		var err error
		relative, err = filepath.Rel(manager.rootPath, filepath.Clean(path))
		if err != nil {
			return "", &core.AppError{Code: core.CodeAccessDenied, Message: "requested path is outside the configured root"}
		}
	} else {
		relative = filepath.Clean(path)
	}
	if !filepath.IsLocal(relative) {
		return "", &core.AppError{Code: core.CodeAccessDenied, Message: "requested path is outside the configured root"}
	}
	return relative, nil
}

func (manager *Manager) openError(err error) *core.AppError {
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrClosed) {
		return &core.AppError{Code: core.CodeIO, Message: "cannot open requested source", Cause: err}
	}
	// os.Root does not export a path-escape sentinel. Treat an unsuccessful
	// confined open as an access refusal rather than inspecting error text.
	return &core.AppError{Code: core.CodeAccessDenied, Message: "requested path cannot be opened within the configured root", Cause: err}
}

type Lease struct {
	manager  *Manager
	handle   *handle
	released bool
}

func (manager *Manager) Acquire(ctx context.Context, id string) (*Lease, *core.AppError) {
	if err := ctx.Err(); err != nil {
		return nil, core.ContextError(err, manager.limits.MaxScanTime)
	}
	manager.mu.Lock()
	handle := manager.handles[id]
	if handle == nil || manager.closed {
		manager.mu.Unlock()
		return nil, &core.AppError{Code: core.CodeHandleExpired, Message: "file handle is no longer available"}
	}
	manager.mu.Unlock()
	// A handle has one scan position. Reject competing use immediately; callers
	// may submit another operation after the current lease is released.
	if !handle.mu.TryLock() {
		return nil, &core.AppError{Code: core.CodeResourceLimit, Message: "source is already in use", Limit: &core.LimitDetail{Name: "source_busy", Limit: 1, Value: 2}}
	}
	if err := ctx.Err(); err != nil {
		handle.mu.Unlock()
		return nil, core.ContextError(err, manager.limits.MaxScanTime)
	}
	if handle.closed || !manager.now().Before(handle.expires) {
		handle.closed = true
		_ = handle.file.Close()
		handle.mu.Unlock()
		manager.mu.Lock()
		if manager.handles[id] == handle {
			delete(manager.handles, id)
		}
		manager.mu.Unlock()
		return nil, &core.AppError{Code: core.CodeHandleExpired, Message: "file handle has expired"}
	}
	lease := &Lease{manager: manager, handle: handle}
	if err := lease.Validate(); err != nil {
		lease.Release()
		return nil, err
	}
	if _, seekErr := handle.file.Seek(0, io.SeekStart); seekErr != nil {
		handle.mu.Unlock()
		return nil, &core.AppError{Code: core.CodeIO, Message: "cannot rewind source"}
	}
	handle.expires = manager.now().Add(manager.limits.HandleTTL)
	return lease, nil
}

func (lease *Lease) File() *os.File      { return lease.handle.file }
func (lease *Lease) Format() core.Format { return lease.handle.format }
func (lease *Lease) Ephemeral() bool     { return lease.handle.ephemeral }

// Validate must run under the lease both before and after scanning. Detected
// mutation invalidates the handle immediately, including for a final page.
func (lease *Lease) Validate() *core.AppError {
	if lease.released {
		return &core.AppError{Code: core.CodeInternal, Message: "source lease has been released"}
	}
	handle := lease.handle
	fileInfo, fileErr := handle.file.Stat()
	pathInfo, pathErr := lease.manager.root.Stat(handle.relPath)
	if fileErr == nil && pathErr == nil && !handle.closed && os.SameFile(handle.original, fileInfo) && os.SameFile(handle.original, pathInfo) && fingerprintOf(fileInfo) == handle.fingerprint && fingerprintOf(pathInfo) == handle.fingerprint {
		return nil
	}
	handle.closed = true
	_ = handle.file.Close()
	lease.manager.mu.Lock()
	if lease.manager.handles[handle.id] == handle {
		delete(lease.manager.handles, handle.id)
	}
	lease.manager.mu.Unlock()
	return &core.AppError{Code: core.CodeSourceChanged, Message: "source changed after it was opened"}
}

func (lease *Lease) Release() {
	if lease == nil || lease.released {
		return
	}
	lease.released = true
	if !lease.handle.closed {
		lease.handle.expires = lease.manager.now().Add(lease.manager.limits.HandleTTL)
	}
	lease.handle.mu.Unlock()
}

func (manager *Manager) CloseHandle(id string) (bool, *core.AppError) {
	manager.mu.Lock()
	handle := manager.handles[id]
	if handle != nil {
		delete(manager.handles, id)
	}
	manager.mu.Unlock()
	if handle == nil {
		return false, nil
	}
	handle.mu.Lock()
	defer handle.mu.Unlock()
	if handle.closed {
		return false, nil
	}
	handle.closed = true
	if err := handle.file.Close(); err != nil {
		return true, &core.AppError{Code: core.CodeIO, Message: "failed to close file handle"}
	}
	return true, nil
}

func (manager *Manager) ActiveHandles() int {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return len(manager.handles)
}

func (manager *Manager) cleanupExpired() {
	manager.mu.Lock()
	type candidate struct {
		id     string
		handle *handle
	}
	candidates := make([]candidate, 0, len(manager.handles))
	for id, handle := range manager.handles {
		candidates = append(candidates, candidate{id: id, handle: handle})
	}
	manager.mu.Unlock()
	for _, candidate := range candidates {
		handle := candidate.handle
		if !handle.mu.TryLock() {
			continue
		}
		expired := !handle.closed && !manager.now().Before(handle.expires)
		if expired {
			handle.closed = true
			_ = handle.file.Close()
		}
		handle.mu.Unlock()
		if expired {
			manager.mu.Lock()
			if manager.handles[candidate.id] == handle {
				delete(manager.handles, candidate.id)
			}
			manager.mu.Unlock()
		}
	}
}

func (manager *Manager) Shutdown() error {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return nil
	}
	manager.closed = true
	handles := make([]*handle, 0, len(manager.handles))
	for _, handle := range manager.handles {
		handles = append(handles, handle)
	}
	manager.handles = make(map[string]*handle)
	manager.mu.Unlock()
	var firstErr error
	for _, handle := range handles {
		handle.mu.Lock()
		if !handle.closed {
			handle.closed = true
			if err := handle.file.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		handle.mu.Unlock()
	}
	if err := manager.root.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func newID(prefix string) (string, error) {
	var random [12]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(random[:]), nil
}

// Package gguf reads the header and the metadata key-value section of a GGUF file.
//
// The format is described in https://github.com/ggml-org/ggml/blob/master/docs/gguf.md.
// Only little-endian files are supported. A big-endian file has the same magic bytes, but its
// version field reads as a huge number, so ReadMeta rejects it with an unsupported-version error.
// Tensor data is never read.
//
// Values of arrays are skipped without allocation. Strings longer than maxStringValueLen are
// skipped as well. Every length read from the file is checked against a limit before it is used.
package gguf

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
)

// Limits that bound the work and memory used for untrusted input.
const (
	maxKeyLen         = 65535   // Longest key name in bytes, as the spec allows.
	maxStringValueLen = 1 << 20 // Longest string value kept in memory. Longer values are skipped.
	maxKVCount        = 1 << 16 // Largest metadata_kv_count accepted.
	maxArrayDepth     = 4       // Deepest array nesting accepted.
	maxCandidates     = 16      // Most "<prefix>.<key>" integer candidates kept before general.architecture is seen.
)

// Metadata value type numbers from the GGUF specification.
const (
	typeUint8   uint32 = 0
	typeInt8    uint32 = 1
	typeUint16  uint32 = 2
	typeInt16   uint32 = 3
	typeUint32  uint32 = 4
	typeInt32   uint32 = 5
	typeFloat32 uint32 = 6
	typeBool    uint32 = 7
	typeString  uint32 = 8
	typeArray   uint32 = 9
	typeUint64  uint32 = 10
	typeInt64   uint32 = 11
	typeFloat64 uint32 = 12
)

// ErrInvalid is wrapped by errors that report malformed GGUF data.
// I/O errors such as io.ErrUnexpectedEOF are returned wrapped too, so check them separately if needed.
var ErrInvalid = errors.New("invalid GGUF data")

// Meta is the metadata that this package extracts from a GGUF file.
//
// A numeric field is valid only when its Has field is true. A string field is empty when the key is absent.
type Meta struct {
	// Version is the GGUF format version, 2 or 3.
	Version uint32
	// TensorCount is tensor_count from the header. It is not checked against the file contents.
	TensorCount uint64
	// KVCount is metadata_kv_count from the header.
	KVCount uint64

	// Architecture is general.architecture.
	Architecture string
	// Name is general.name.
	Name string
	// License is general.license, an SPDX expression.
	License string

	// FileType is general.file_type. Use FileTypeName for a readable name.
	FileType uint32
	// HasFileType reports whether general.file_type was present.
	HasFileType bool

	// BlockCount is "<architecture>.block_count".
	BlockCount uint64
	// HasBlockCount reports whether the block count was present.
	HasBlockCount bool

	// ContextLength is "<architecture>.context_length".
	ContextLength uint64
	// HasContextLength reports whether the context length was present.
	HasContextLength bool

	// EmbeddingLength is "<architecture>.embedding_length".
	EmbeddingLength uint64
	// HasEmbeddingLength reports whether the embedding length was present.
	HasEmbeddingLength bool
}

// fileTypeNames maps the general.file_type numbers listed in the project's research notes to their names.
// Other numbers are reported as unknown(<n>) by FileTypeName.
var fileTypeNames = map[uint32]string{
	1:  "MOSTLY_F16",
	2:  "MOSTLY_Q4_0",
	7:  "MOSTLY_Q8_0",
	15: "MOSTLY_Q4_K_M",
	18: "MOSTLY_Q6_K",
}

// FileTypeName returns the name of general.file_type.
// It returns an empty string when the key was absent. It returns "unknown(<n>)" for a number without a name here.
func (m Meta) FileTypeName() string {
	if !m.HasFileType {
		return ""
	}
	if name, ok := fileTypeNames[m.FileType]; ok {
		return name
	}
	return fmt.Sprintf("unknown(%d)", m.FileType)
}

// ReadMeta reads the GGUF header and the metadata key-value section from r.
//
// It stops as soon as every field of Meta is known, so it does not need to read the whole file.
// It returns an error if general.architecture is missing, because the architecture-specific keys cannot be named without it.
func ReadMeta(r io.Reader) (Meta, error) {
	p := &parser{r: bufio.NewReaderSize(r, 64<<10)}

	var magic [4]byte
	if err := p.readFull(magic[:]); err != nil {
		return Meta{}, fmt.Errorf("gguf: read magic: %w", err)
	}
	if string(magic[:]) != "GGUF" {
		return Meta{}, fmt.Errorf("gguf: %w: bad magic", ErrInvalid)
	}

	version, err := p.readU32()
	if err != nil {
		return Meta{}, fmt.Errorf("gguf: read version: %w", err)
	}
	if version != 2 && version != 3 {
		return Meta{}, fmt.Errorf("gguf: %w: unsupported version %d", ErrInvalid, version)
	}
	tensorCount, err := p.readU64()
	if err != nil {
		return Meta{}, fmt.Errorf("gguf: read tensor count: %w", err)
	}
	kvCount, err := p.readU64()
	if err != nil {
		return Meta{}, fmt.Errorf("gguf: read metadata count: %w", err)
	}
	if kvCount > maxKVCount {
		return Meta{}, fmt.Errorf("gguf: %w: metadata_kv_count %d exceeds %d", ErrInvalid, kvCount, maxKVCount)
	}

	b := &builder{
		meta: Meta{Version: version, TensorCount: tensorCount, KVCount: kvCount},
	}
	for i := uint64(0); i < kvCount; i++ {
		key, err := p.readKey()
		if err != nil {
			return Meta{}, fmt.Errorf("gguf: metadata %d: %w", i, err)
		}
		typ, err := p.readU32()
		if err != nil {
			return Meta{}, fmt.Errorf("gguf: metadata %q: read type: %w", key, err)
		}
		if err := b.handle(p, key, typ); err != nil {
			return Meta{}, fmt.Errorf("gguf: metadata %q: %w", key, err)
		}
		if b.complete() {
			break
		}
	}
	if b.meta.Architecture == "" {
		return Meta{}, fmt.Errorf("gguf: %w: general.architecture is missing", ErrInvalid)
	}
	return b.meta, nil
}

// parser reads little-endian primitives from a buffered reader.
type parser struct {
	r   *bufio.Reader
	buf [8]byte
}

// readFull fills b and maps a short read to io.ErrUnexpectedEOF.
func (p *parser) readFull(b []byte) error {
	if _, err := io.ReadFull(p.r, b); err != nil {
		if errors.Is(err, io.EOF) {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return nil
}

func (p *parser) readU32() (uint32, error) {
	if err := p.readFull(p.buf[:4]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(p.buf[:4]), nil
}

func (p *parser) readU64() (uint64, error) {
	if err := p.readFull(p.buf[:8]); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(p.buf[:8]), nil
}

// discard skips n bytes without storing them.
func (p *parser) discard(n uint64) error {
	if n > math.MaxInt64 {
		return fmt.Errorf("gguf: %w: skip length %d is too large", ErrInvalid, n)
	}
	copied, err := io.CopyN(io.Discard, p.r, int64(n))
	if err != nil {
		if errors.Is(err, io.EOF) && copied < int64(n) {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return nil
}

// readKey reads a key string and checks its length against maxKeyLen.
func (p *parser) readKey() (string, error) {
	n, err := p.readU64()
	if err != nil {
		return "", fmt.Errorf("read key length: %w", err)
	}
	if n > maxKeyLen {
		return "", fmt.Errorf("gguf: %w: key length %d exceeds %d", ErrInvalid, n, maxKeyLen)
	}
	buf := make([]byte, n)
	if err := p.readFull(buf); err != nil {
		return "", fmt.Errorf("read key: %w", err)
	}
	return string(buf), nil
}

// readString reads a string value. It returns kept=false and skips the bytes if the value is longer than maxStringValueLen.
func (p *parser) readString() (s string, kept bool, err error) {
	n, err := p.readU64()
	if err != nil {
		return "", false, fmt.Errorf("read string length: %w", err)
	}
	if n > maxStringValueLen {
		if err := p.discard(n); err != nil {
			return "", false, err
		}
		return "", false, nil
	}
	buf := make([]byte, n)
	if err := p.readFull(buf); err != nil {
		return "", false, fmt.Errorf("read string: %w", err)
	}
	return string(buf), true, nil
}

// fixedSize returns the byte size of a scalar type. It returns false for strings and arrays.
func fixedSize(typ uint32) (int, bool) {
	switch typ {
	case typeUint8, typeInt8, typeBool:
		return 1, true
	case typeUint16, typeInt16:
		return 2, true
	case typeUint32, typeInt32, typeFloat32:
		return 4, true
	case typeUint64, typeInt64, typeFloat64:
		return 8, true
	}
	return 0, false
}

// readInt reads an integer value of any integer type. A negative signed value is an error.
func (p *parser) readInt(typ uint32) (uint64, error) {
	if !isInteger(typ) {
		return 0, fmt.Errorf("gguf: %w: value type %d is not an integer", ErrInvalid, typ)
	}
	size, _ := fixedSize(typ)
	if err := p.readFull(p.buf[:size]); err != nil {
		return 0, err
	}
	switch typ {
	case typeUint8:
		return uint64(p.buf[0]), nil
	case typeUint16:
		return uint64(binary.LittleEndian.Uint16(p.buf[:2])), nil
	case typeUint32:
		return uint64(binary.LittleEndian.Uint32(p.buf[:4])), nil
	case typeUint64:
		return binary.LittleEndian.Uint64(p.buf[:8]), nil
	case typeInt8:
		if v := int8(p.buf[0]); v < 0 {
			return 0, fmt.Errorf("gguf: %w: negative value %d", ErrInvalid, v)
		}
		return uint64(p.buf[0]), nil
	case typeInt16:
		v := int16(binary.LittleEndian.Uint16(p.buf[:2]))
		if v < 0 {
			return 0, fmt.Errorf("gguf: %w: negative value %d", ErrInvalid, v)
		}
		return uint64(v), nil
	case typeInt32:
		v := int32(binary.LittleEndian.Uint32(p.buf[:4]))
		if v < 0 {
			return 0, fmt.Errorf("gguf: %w: negative value %d", ErrInvalid, v)
		}
		return uint64(v), nil
	case typeInt64:
		v := int64(binary.LittleEndian.Uint64(p.buf[:8]))
		if v < 0 {
			return 0, fmt.Errorf("gguf: %w: negative value %d", ErrInvalid, v)
		}
		return uint64(v), nil
	}
	return 0, fmt.Errorf("gguf: %w: unhandled integer type %d", ErrInvalid, typ)
}

// skipPayload skips one value of the given type, without its type header.
// depth is the number of arrays that enclose this value.
func (p *parser) skipPayload(typ uint32, depth int) error {
	if size, ok := fixedSize(typ); ok {
		return p.discard(uint64(size))
	}
	switch typ {
	case typeString:
		n, err := p.readU64()
		if err != nil {
			return fmt.Errorf("read string length: %w", err)
		}
		return p.discard(n)
	case typeArray:
		if depth >= maxArrayDepth {
			return fmt.Errorf("gguf: %w: arrays nested deeper than %d", ErrInvalid, maxArrayDepth)
		}
		elemType, err := p.readU32()
		if err != nil {
			return fmt.Errorf("read array element type: %w", err)
		}
		count, err := p.readU64()
		if err != nil {
			return fmt.Errorf("read array length: %w", err)
		}
		return p.skipElements(elemType, count, depth+1)
	}
	return fmt.Errorf("gguf: %w: unknown value type %d", ErrInvalid, typ)
}

// skipElements skips count elements of elemType. A fixed-size element type is skipped in one step.
func (p *parser) skipElements(elemType uint32, count uint64, depth int) error {
	if size, ok := fixedSize(elemType); ok {
		if count > uint64(math.MaxInt64)/uint64(size) {
			return fmt.Errorf("gguf: %w: array byte size overflows", ErrInvalid)
		}
		return p.discard(count * uint64(size))
	}
	// Strings and nested arrays have variable size. Each element reads at least its length
	// field, so the loop ends at end of input even if count is large.
	for i := uint64(0); i < count; i++ {
		if err := p.skipPayload(elemType, depth); err != nil {
			return err
		}
	}
	return nil
}

// builder assembles Meta from the key-value pairs as they are read.
type builder struct {
	meta Meta

	nameSeen    bool
	licenseSeen bool
	typeSeen    bool

	// candidates holds integer values of "<prefix>.block_count" and similar keys seen before
	// general.architecture. They are matched once the architecture is known.
	candidates map[string]uint64
}

// archSuffixes are the architecture-specific key suffixes that this package reads.
var archSuffixes = []string{".block_count", ".context_length", ".embedding_length"}

// complete reports whether every field has been found, so reading can stop early.
func (b *builder) complete() bool {
	return b.meta.Architecture != "" && b.nameSeen && b.licenseSeen && b.typeSeen &&
		b.meta.HasBlockCount && b.meta.HasContextLength && b.meta.HasEmbeddingLength
}

// handle reads the value of one key-value pair. It stores the value if the key is one that this package reads.
func (b *builder) handle(p *parser, key string, typ uint32) error {
	switch key {
	case "general.architecture":
		return b.setArchitecture(p, typ)
	case "general.name":
		s, err := b.readStringField(p, typ, "general.name")
		if err != nil {
			return err
		}
		if b.nameSeen {
			return fmt.Errorf("gguf: %w: duplicate key", ErrInvalid)
		}
		b.meta.Name, b.nameSeen = s, true
		return nil
	case "general.license":
		s, err := b.readStringField(p, typ, "general.license")
		if err != nil {
			return err
		}
		if b.licenseSeen {
			return fmt.Errorf("gguf: %w: duplicate key", ErrInvalid)
		}
		b.meta.License, b.licenseSeen = s, true
		return nil
	case "general.file_type":
		v, err := p.readInt(typ)
		if err != nil {
			return err
		}
		if v > math.MaxUint32 {
			return fmt.Errorf("gguf: %w: general.file_type %d exceeds uint32", ErrInvalid, v)
		}
		if b.typeSeen {
			return fmt.Errorf("gguf: %w: duplicate key", ErrInvalid)
		}
		b.meta.FileType, b.meta.HasFileType, b.typeSeen = uint32(v), true, true
		return nil
	}

	suffix, ok := archSuffix(key)
	if !ok {
		return skipValue(p, typ)
	}
	if b.meta.Architecture != "" {
		if key != b.meta.Architecture+suffix {
			return skipValue(p, typ)
		}
		v, err := p.readInt(typ)
		if err != nil {
			return fmt.Errorf("architecture key: %w", err)
		}
		return b.setArchValue(suffix, v)
	}

	// The architecture is not known yet. Keep integer candidates and skip the rest.
	if !isInteger(typ) {
		return skipValue(p, typ)
	}
	v, err := p.readInt(typ)
	if err != nil {
		return err
	}
	if b.candidates == nil {
		b.candidates = make(map[string]uint64)
	}
	if _, dup := b.candidates[key]; dup {
		return fmt.Errorf("gguf: %w: duplicate key", ErrInvalid)
	}
	if len(b.candidates) >= maxCandidates {
		return fmt.Errorf("gguf: %w: too many architecture-like keys before general.architecture", ErrInvalid)
	}
	b.candidates[key] = v
	return nil
}

// setArchitecture reads general.architecture and then fills the architecture-specific fields from the candidates.
func (b *builder) setArchitecture(p *parser, typ uint32) error {
	s, err := b.readStringField(p, typ, "general.architecture")
	if err != nil {
		return err
	}
	if b.meta.Architecture != "" {
		return fmt.Errorf("gguf: %w: duplicate key", ErrInvalid)
	}
	if s == "" {
		return fmt.Errorf("gguf: %w: general.architecture is empty", ErrInvalid)
	}
	b.meta.Architecture = s
	for _, suffix := range archSuffixes {
		if v, ok := b.candidates[s+suffix]; ok {
			if err := b.setArchValue(suffix, v); err != nil {
				return err
			}
		}
	}
	b.candidates = nil
	return nil
}

// readStringField reads a string-typed key that must be kept. A value longer than maxStringValueLen is an error.
func (b *builder) readStringField(p *parser, typ uint32, key string) (string, error) {
	if typ != typeString {
		return "", fmt.Errorf("gguf: %w: %s must be a string", ErrInvalid, key)
	}
	s, kept, err := p.readString()
	if err != nil {
		return "", err
	}
	if !kept {
		return "", fmt.Errorf("gguf: %w: %s is longer than %d bytes", ErrInvalid, key, maxStringValueLen)
	}
	return s, nil
}

// setArchValue stores an architecture-specific integer. A second value for the same field is an error.
func (b *builder) setArchValue(suffix string, v uint64) error {
	switch suffix {
	case ".block_count":
		if b.meta.HasBlockCount {
			return fmt.Errorf("gguf: %w: duplicate block count", ErrInvalid)
		}
		b.meta.BlockCount, b.meta.HasBlockCount = v, true
	case ".context_length":
		if b.meta.HasContextLength {
			return fmt.Errorf("gguf: %w: duplicate context length", ErrInvalid)
		}
		b.meta.ContextLength, b.meta.HasContextLength = v, true
	case ".embedding_length":
		if b.meta.HasEmbeddingLength {
			return fmt.Errorf("gguf: %w: duplicate embedding length", ErrInvalid)
		}
		b.meta.EmbeddingLength, b.meta.HasEmbeddingLength = v, true
	}
	return nil
}

// archSuffix reports which architecture-specific suffix key has, if any.
func archSuffix(key string) (string, bool) {
	for _, suffix := range archSuffixes {
		if strings.HasSuffix(key, suffix) && len(key) > len(suffix) {
			return suffix, true
		}
	}
	return "", false
}

// isInteger reports whether typ is an integer type.
func isInteger(typ uint32) bool {
	switch typ {
	case typeUint8, typeInt8, typeUint16, typeInt16, typeUint32, typeInt32, typeUint64, typeInt64:
		return true
	}
	return false
}

// skipValue skips a value of the given type, which has no key-specific handling.
func skipValue(p *parser, typ uint32) error {
	return p.skipPayload(typ, 0)
}

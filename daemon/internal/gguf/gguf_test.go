package gguf

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

// kv is one metadata key-value pair as written by the test builder.
type kv struct {
	key     string
	typ     uint32
	payload []byte
}

func le32(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

func le64(v uint64) []byte {
	b := make([]byte, 8)
	binary.LittleEndian.PutUint64(b, v)
	return b
}

// strPayload encodes a string value: uint64 length followed by the bytes.
func strPayload(s string) []byte {
	return append(le64(uint64(len(s))), s...)
}

// arrPayload encodes an array value: element type, element count, then the element bodies.
func arrPayload(elemType uint32, count uint64, body []byte) []byte {
	b := append(le32(elemType), le64(count)...)
	return append(b, body...)
}

// header encodes the file header with explicit counts, so tests can set counts that do not match.
func header(version uint32, tensorCount, kvCount uint64) []byte {
	b := []byte("GGUF")
	b = append(b, le32(version)...)
	b = append(b, le64(tensorCount)...)
	return append(b, le64(kvCount)...)
}

// buildFile encodes a GGUF file with the given key-value pairs in order.
func buildFile(version uint32, kvs ...kv) []byte {
	b := header(version, 0, uint64(len(kvs)))
	for _, k := range kvs {
		b = append(b, le64(uint64(len(k.key)))...)
		b = append(b, k.key...)
		b = append(b, le32(k.typ)...)
		b = append(b, k.payload...)
	}
	return b
}

// bigTokens is a string array of 262144 short tokens, like the one in the gemma3 sample.
func bigTokens() kv {
	const n = 262144
	var body bytes.Buffer
	for i := 0; i < n; i++ {
		body.Write(le64(1))
		body.WriteByte('t')
	}
	return kv{"tokenizer.ggml.tokens", typeArray, arrPayload(typeString, n, body.Bytes())}
}

// gemmaKVs returns the fields of gemma-3-1b-it-Q4_K_M from the research notes.
func gemmaKVs() map[string]kv {
	return map[string]kv{
		"arch":    {"general.architecture", typeString, strPayload("gemma3")},
		"name":    {"general.name", typeString, strPayload("Gemma 3 1b It")},
		"license": {"general.license", typeString, strPayload("gemma")},
		"ctx":     {"gemma3.context_length", typeUint32, le32(32768)},
		"emb":     {"gemma3.embedding_length", typeUint32, le32(1152)},
		"blk":     {"gemma3.block_count", typeUint32, le32(26)},
		"qv":      {"general.quantization_version", typeUint32, le32(2)},
		"ft":      {"general.file_type", typeUint32, le32(15)},
	}
}

// wantGemma is the Meta that the gemma3 sample must produce.
var wantGemma = Meta{
	Version:            3,
	TensorCount:        0,
	KVCount:            0,
	Architecture:       "gemma3",
	Name:               "Gemma 3 1b It",
	License:            "gemma",
	FileType:           15,
	HasFileType:        true,
	BlockCount:         26,
	HasBlockCount:      true,
	ContextLength:      32768,
	HasContextLength:   true,
	EmbeddingLength:    1152,
	HasEmbeddingLength: true,
}

// sameMeta compares the fields that the table tests check, ignoring the header counts.
func sameMeta(got, want Meta) bool {
	got.KVCount, got.TensorCount = 0, 0
	want.KVCount, want.TensorCount = 0, 0
	return got == want
}

func TestReadMetaGemma3(t *testing.T) {
	g := gemmaKVs()
	tests := []struct {
		name string
		kvs  []kv
	}{
		{
			name: "architecture first with a large array between the fields",
			kvs:  []kv{g["arch"], g["name"], g["license"], g["ctx"], g["emb"], g["blk"], bigTokens(), g["qv"], g["ft"]},
		},
		{
			name: "architecture-specific keys before general.architecture",
			kvs:  []kv{g["ctx"], g["emb"], g["blk"], bigTokens(), g["qv"], g["ft"], g["name"], g["license"], g["arch"]},
		},
		{
			name: "file_type after the large array with unrelated keys",
			kvs:  []kv{g["arch"], bigTokens(), g["name"], g["license"], g["ctx"], g["emb"], g["blk"], g["ft"], g["qv"]},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := buildFile(3, tt.kvs...)
			m, err := ReadMeta(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("ReadMeta: %v", err)
			}
			if !sameMeta(m, wantGemma) {
				t.Fatalf("ReadMeta = %+v\nwant %+v", m, wantGemma)
			}
			if m.Version != 3 || m.KVCount != uint64(len(tt.kvs)) {
				t.Fatalf("header: version %d kv %d", m.Version, m.KVCount)
			}
			if got := m.FileTypeName(); got != "MOSTLY_Q4_K_M" {
				t.Fatalf("FileTypeName = %q", got)
			}
		})
	}
}

func TestReadMetaTable(t *testing.T) {
	g := gemmaKVs()
	nested := func(depth int) kv {
		// Build an array nested depth levels deep, with the innermost array holding one uint8.
		payload := append(le32(typeUint8), le64(1)...)
		payload = append(payload, 0x7)
		for i := 1; i < depth; i++ {
			payload = arrPayload(typeArray, 1, payload)
		}
		return kv{"x.nested", typeArray, payload}
	}
	tests := []struct {
		name string
		data []byte
		want Meta
	}{
		{
			name: "version 2 accepted",
			data: buildFile(2, g["arch"], g["blk"], g["ctx"], g["emb"], g["name"], g["license"], g["ft"]),
			want: Meta{Version: 2, Architecture: "gemma3", Name: "Gemma 3 1b It", License: "gemma",
				FileType: 15, HasFileType: true, BlockCount: 26, HasBlockCount: true,
				ContextLength: 32768, HasContextLength: true, EmbeddingLength: 1152, HasEmbeddingLength: true},
		},
		{
			name: "other architecture keys are ignored",
			data: buildFile(3, kv{"llama.block_count", typeUint32, le32(99)}, g["arch"], g["blk"], g["ctx"], g["emb"], g["name"], g["license"], g["ft"]),
			want: wantGemma,
		},
		{
			name: "uint64 and non-negative int64 values are accepted",
			data: buildFile(3, g["arch"],
				kv{"gemma3.block_count", typeUint64, le64(26)},
				kv{"gemma3.context_length", typeInt64, le64(32768)},
				kv{"gemma3.embedding_length", typeInt32, le32(1152)},
				g["name"], g["license"], kv{"general.file_type", typeUint64, le64(15)}),
			want: wantGemma,
		},
		{
			name: "unrelated string value over 1 MiB is skipped",
			data: buildFile(3, g["arch"], kv{"tokenizer.chat_template", typeString, strPayload(strings.Repeat("a", maxStringValueLen+1))},
				g["blk"], g["ctx"], g["emb"], g["name"], g["license"], g["ft"]),
			want: wantGemma,
		},
		{
			name: "nested arrays up to the depth limit are skipped",
			data: buildFile(3, nested(maxArrayDepth), g["arch"], g["blk"], g["ctx"], g["emb"], g["name"], g["license"], g["ft"]),
			want: wantGemma,
		},
		{
			name: "string array is skipped element by element",
			data: buildFile(3, g["arch"], kv{"x.strs", typeArray, arrPayload(typeString, 3,
				append(append(strPayload("a"), strPayload("")...), strPayload("bcd")...))},
				g["blk"], g["ctx"], g["emb"], g["name"], g["license"], g["ft"]),
			want: wantGemma,
		},
		{
			name: "float and bool values are skipped",
			data: buildFile(3, g["arch"], kv{"x.f", typeFloat64, make([]byte, 8)}, kv{"x.b", typeBool, []byte{1}},
				kv{"x.f32arr", typeArray, arrPayload(typeFloat32, 2, make([]byte, 8))},
				g["blk"], g["ctx"], g["emb"], g["name"], g["license"], g["ft"]),
			want: wantGemma,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := ReadMeta(bytes.NewReader(tt.data))
			if err != nil {
				t.Fatalf("ReadMeta: %v", err)
			}
			if !sameMeta(m, tt.want) {
				t.Fatalf("ReadMeta = %+v\nwant %+v", m, tt.want)
			}
		})
	}
}

func TestReadMetaStopsEarly(t *testing.T) {
	// Every field is known after general.file_type. The bytes after it are never read,
	// so a file that is cut off after that point is still accepted.
	g := gemmaKVs()
	data := buildFile(3, g["arch"], g["name"], g["license"], g["ctx"], g["emb"], g["blk"], g["ft"], g["qv"])
	// The last pair is general.quantization_version: 8 bytes of key length, the key, 4 bytes of type, 4 bytes of value.
	qvSize := 8 + len(g["qv"].key) + 4 + len(g["qv"].payload)
	cut := data[:len(data)-qvSize]
	m, err := ReadMeta(bytes.NewReader(cut))
	if err != nil {
		t.Fatalf("ReadMeta of a file cut after the last needed field: %v", err)
	}
	if !sameMeta(m, wantGemma) {
		t.Fatalf("ReadMeta = %+v", m)
	}
}

func TestReadMetaTruncated(t *testing.T) {
	g := gemmaKVs()
	data := buildFile(3, g["arch"], bigTokens(), g["name"], g["license"], g["ctx"], g["emb"], g["blk"], g["ft"])
	cuts := map[string]int{
		"empty":            0,
		"inside magic":     2,
		"inside header":    20,
		"after header":     24,
		"inside key":       30,
		"middle of tokens": len(data) / 2,
		"inside file_type": len(data) - 2,
	}
	for name, n := range cuts {
		t.Run(name, func(t *testing.T) {
			_, err := ReadMeta(bytes.NewReader(data[:n]))
			if err == nil {
				t.Fatalf("ReadMeta of %d bytes: want error", n)
			}
		})
	}
}

func TestReadMetaErrors(t *testing.T) {
	g := gemmaKVs()
	tooLargeKV := uint64(maxKVCount + 1)
	badMagic := buildFile(3, g["arch"])
	copy(badMagic, "GGVF")

	tests := []struct {
		name string
		data []byte
	}{
		{"bad magic", badMagic},
		{"version 1", buildFile(1, g["arch"])},
		{"version 4", buildFile(4, g["arch"])},
		{"big-endian version bytes", append([]byte("GGUF"), 0, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0)},
		{"kv count above limit", header(3, 0, tooLargeKV)},
		{"key length above limit", append(append(header(3, 0, 1), le64(maxKeyLen+1)...), make([]byte, 4)...)},
		{"huge key length", append(header(3, 0, 1), le64(1<<60)...)},
		{"huge string length for unknown key", append(append(append(header(3, 0, 1), le64(1)...), 'x'), append(le32(typeString), le64(1<<62)...)...)},
		{"general.name over 1 MiB", buildFile(3, g["arch"], kv{"general.name", typeString, strPayload(strings.Repeat("n", maxStringValueLen+1))})},
		{"array byte size overflows", buildFile(3, g["arch"], kv{"x.big", typeArray, arrPayload(typeUint32, 1<<61, nil)})},
		{"arrays nested too deep", buildFile(3, g["arch"], kv{"x.nested", typeArray, deepNest(maxArrayDepth + 1)})},
		{"negative block count", buildFile(3, g["arch"], kv{"gemma3.block_count", typeInt32, le32(0xffffffff)})},
		{"architecture is not a string", buildFile(3, kv{"general.architecture", typeUint32, le32(1)})},
		{"architecture missing", buildFile(3, g["name"], g["ft"])},
		{"architecture empty", buildFile(3, kv{"general.architecture", typeString, strPayload("")})},
		{"duplicate architecture", buildFile(3, g["arch"], g["arch"])},
		{"block count is a string", buildFile(3, g["arch"], kv{"gemma3.block_count", typeString, strPayload("26")})},
		{"file_type above uint32", buildFile(3, g["arch"], kv{"general.file_type", typeUint64, le64(1 << 32)})},
		{"unknown value type", buildFile(3, g["arch"], kv{"x.bad", 99, nil})},
		{"too many architecture-like keys before the architecture", buildFile(3, manyCandidates(maxCandidates+1)...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ReadMeta(bytes.NewReader(tt.data)); err == nil {
				t.Fatal("ReadMeta: want error")
			}
		})
	}
}

// deepNest builds an array payload nested depth levels deep, with a single element at the bottom.
func deepNest(depth int) []byte {
	payload := append(le32(typeUint8), le64(1)...)
	payload = append(payload, 0x7)
	for i := 1; i < depth; i++ {
		payload = arrPayload(typeArray, 1, payload)
	}
	return payload
}

// manyCandidates returns n integer keys that end in .block_count, followed by the architecture.
func manyCandidates(n int) []kv {
	kvs := make([]kv, 0, n+1)
	for i := 0; i < n; i++ {
		kvs = append(kvs, kv{"p" + strings.Repeat("x", i%3) + string(rune('a'+i%20)) + string(rune('a'+i/20)) + ".block_count", typeUint32, le32(1)})
	}
	return append(kvs, kv{"general.architecture", typeString, strPayload("gemma3")})
}

func TestReadMetaDepthLimitIsExact(t *testing.T) {
	g := gemmaKVs()
	ok := buildFile(3, g["arch"], kv{"x.nested", typeArray, deepNest(maxArrayDepth)}, g["blk"], g["ctx"], g["emb"], g["name"], g["license"], g["ft"])
	if _, err := ReadMeta(bytes.NewReader(ok)); err != nil {
		t.Fatalf("nesting at the limit: %v", err)
	}
}

func TestReadMetaErrWrapsInvalid(t *testing.T) {
	_, err := ReadMeta(bytes.NewReader(buildFile(1, gemmaKVs()["arch"])))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported version error %v does not wrap ErrInvalid", err)
	}
	_, err = ReadMeta(bytes.NewReader([]byte("GGUF\x03")))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated header error %v does not wrap io.ErrUnexpectedEOF", err)
	}
}

func TestFileTypeName(t *testing.T) {
	tests := []struct {
		name string
		m    Meta
		want string
	}{
		{"absent", Meta{}, ""},
		{"F16", Meta{FileType: 1, HasFileType: true}, "MOSTLY_F16"},
		{"Q4_0", Meta{FileType: 2, HasFileType: true}, "MOSTLY_Q4_0"},
		{"Q8_0", Meta{FileType: 7, HasFileType: true}, "MOSTLY_Q8_0"},
		{"Q4_K_M", Meta{FileType: 15, HasFileType: true}, "MOSTLY_Q4_K_M"},
		{"Q6_K", Meta{FileType: 18, HasFileType: true}, "MOSTLY_Q6_K"},
		{"not in table", Meta{FileType: 3, HasFileType: true}, "unknown(3)"},
		{"zero is not in table", Meta{FileType: 0, HasFileType: true}, "unknown(0)"},
		{"large", Meta{FileType: 4000000000, HasFileType: true}, "unknown(4000000000)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.FileTypeName(); got != tt.want {
				t.Fatalf("FileTypeName = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestReadMetaLocalSample reads a real GGUF file named by LIGHTLIX_GGUF_SAMPLE. It is skipped when the variable is unset.
// It does not use the network.
func TestReadMetaLocalSample(t *testing.T) {
	path := os.Getenv("LIGHTLIX_GGUF_SAMPLE")
	if path == "" {
		t.Skip("LIGHTLIX_GGUF_SAMPLE is not set")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := ReadMeta(f)
	if err != nil {
		t.Fatalf("ReadMeta(%s): %v", path, err)
	}
	if !sameMeta(m, wantGemma) {
		t.Fatalf("ReadMeta(%s) = %+v\nwant %+v", path, m, wantGemma)
	}
}

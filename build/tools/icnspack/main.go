package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
)

var expectedSizes = map[string]int{
	"icp4": 16,
	"icp5": 32,
	"icp6": 64,
	"ic07": 128,
	"ic08": 256,
	"ic09": 512,
	"ic10": 1024,
}

type iconChunk struct {
	kind string
	data []byte
}

func main() {
	if len(os.Args) < 3 {
		fatalf("usage: icnspack <output.icns> <type=icon.png>...")
	}

	chunks := make([]iconChunk, 0, len(os.Args)-2)
	seen := make(map[string]struct{})
	for _, argument := range os.Args[2:] {
		kind, source, ok := strings.Cut(argument, "=")
		expectedSize, supported := expectedSizes[kind]
		if !ok || !supported || source == "" {
			fatalf("invalid icon chunk %q", argument)
		}
		if _, exists := seen[kind]; exists {
			fatalf("duplicate icon chunk %q", kind)
		}
		seen[kind] = struct{}{}

		data, err := os.ReadFile(source)
		if err != nil {
			fatalf("read %s: %v", source, err)
		}
		configuration, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			fatalf("decode %s: %v", source, err)
		}
		if configuration.Width != expectedSize || configuration.Height != expectedSize {
			fatalf("%s must be %dx%d, got %dx%d", source, expectedSize, expectedSize, configuration.Width, configuration.Height)
		}
		chunks = append(chunks, iconChunk{kind: kind, data: data})
	}

	var contents bytes.Buffer
	contents.WriteString("icns")
	_ = binary.Write(&contents, binary.BigEndian, uint32(0))
	for _, chunk := range chunks {
		contents.WriteString(chunk.kind)
		_ = binary.Write(&contents, binary.BigEndian, uint32(len(chunk.data)+8))
		contents.Write(chunk.data)
	}
	binary.BigEndian.PutUint32(contents.Bytes()[4:8], uint32(contents.Len()))

	output := os.Args[1]
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		fatalf("create output directory: %v", err)
	}
	if err := os.WriteFile(output, contents.Bytes(), 0o644); err != nil {
		fatalf("write %s: %v", output, err)
	}
}

func fatalf(format string, arguments ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", arguments...)
	os.Exit(1)
}

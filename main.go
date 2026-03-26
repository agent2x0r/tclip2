// tclip2 converts macOS .textClipping files to plain text or HTML.
// It supports both the modern data-fork format (binary plist) and the
// legacy resource-fork format.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"howett.net/plist"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: %s [-html] [-txt] file.textClipping [...]\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  -txt   output plain text (default)\n")
		fmt.Fprintf(os.Stderr, "  -html  output HTML\n")
		fmt.Fprintf(os.Stderr, "  -out   output directory (default: same as source file)\n")
		os.Exit(1)
	}

	wantHTML := false
	outDir := ""
	var files []string

	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "-html":
			wantHTML = true
		case "-txt":
			wantHTML = false
		case "-out":
			i++
			if i >= len(os.Args) {
				fmt.Fprintln(os.Stderr, "-out requires a directory argument")
				os.Exit(1)
			}
			outDir = os.Args[i]
		default:
			files = append(files, os.Args[i])
		}
	}

	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "no input files")
		os.Exit(1)
	}

	for _, f := range files {
		if err := convert(f, wantHTML, outDir); err != nil {
			fmt.Fprintf(os.Stderr, "error: %s: %v\n", f, err)
		}
	}
}

func convert(path string, wantHTML bool, outDir string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	stem := strings.TrimSuffix(filepath.Base(path), ".textClipping")
	if outDir == "" {
		outDir = filepath.Dir(path)
	}

	var text, html string

	// Try data fork (binary plist) first — modern format
	if bytes.HasPrefix(data, []byte("bplist00")) {
		text, html, err = fromDataFork(data)
		if err != nil {
			return fmt.Errorf("parsing plist: %w", err)
		}
	}

	// Fall back to resource fork — legacy format
	if text == "" && html == "" {
		text, html, err = fromResourceFork(path)
		if err != nil {
			return fmt.Errorf("parsing resource fork: %w", err)
		}
	}

	if text == "" && html == "" {
		return fmt.Errorf("no text content found")
	}

	if wantHTML {
		if html == "" {
			fmt.Fprintf(os.Stderr, "warn: %s: no HTML, falling back to plain text\n", path)
		} else {
			out := filepath.Join(outDir, stem+".html")
			return os.WriteFile(out, []byte(html), 0644)
		}
	}

	out := filepath.Join(outDir, stem+".txt")
	content := text
	if content == "" {
		content = stripHTML(html)
	}
	return os.WriteFile(out, []byte(content), 0644)
}

// fromDataFork parses the binary plist in the data fork.
// The plist has a "UTI-Data" dict with keys like
// "public.utf8-plain-text" and "public.html".
func fromDataFork(data []byte) (text, html string, err error) {
	var root map[string]interface{}
	_, err = plist.Unmarshal(data, &root)
	if err != nil {
		return
	}

	uti, ok := root["UTI-Data"].(map[string]interface{})
	if !ok {
		return
	}

	text = pickString(uti, "public.utf8-plain-text")
	html = pickString(uti, "public.html")
	if text == "" {
		text = pickString(uti, "public.utf16-plain-text")
	}
	return
}

// fromResourceFork reads the Mac resource fork and extracts TEXT/utf8/HTML resources.
func fromResourceFork(path string) (text, html string, err error) {
	rsrcPath := filepath.Join(path, "..namedfork/rsrc")
	rsrc, err := os.Open(rsrcPath)
	if err != nil {
		return "", "", nil // no resource fork — not an error
	}
	defer rsrc.Close()

	data, err := io.ReadAll(rsrc)
	if err != nil || len(data) < 28 {
		return
	}

	// Resource fork header (data offset, map offset, data size)
	dataOff := binary.BigEndian.Uint32(data[0:4])
	mapOff := binary.BigEndian.Uint32(data[4:8])

	if int(mapOff)+28 > len(data) {
		return
	}

	// Type list is at map+28 (2-byte count, then 8-byte entries)
	typeListStart := int(mapOff) + 28
	numTypes := int(binary.BigEndian.Uint16(data[typeListStart : typeListStart+2]))

	// Build a map of resource type -> data
	resources := make(map[string][]byte)

	for i := 0; i < numTypes; i++ {
		entryOff := typeListStart + 2 + i*8
		if entryOff+8 > len(data) {
			break
		}
		resType := string(data[entryOff : entryOff+4])
		refCount := int(binary.BigEndian.Uint16(data[entryOff+4:entryOff+6])) + 1
		refOff := int(mapOff) + int(binary.BigEndian.Uint16(data[entryOff+6:entryOff+8]))

		// Read first reference entry
		for j := 0; j < refCount; j++ {
			rOff := refOff + j*12
			if rOff+12 > len(data) {
				break
			}
			// bytes 4-7: attributes(1) + data offset(3)
			dOff := int(data[rOff+5])<<16 | int(data[rOff+6])<<8 | int(data[rOff+7])
			absOff := int(dataOff) + dOff
			if absOff+4 > len(data) {
				continue
			}
			dLen := int(binary.BigEndian.Uint32(data[absOff : absOff+4]))
			if absOff+4+dLen > len(data) {
				continue
			}
			resources[resType] = data[absOff+4 : absOff+4+dLen]
		}
	}

	// Prefer utf8 over TEXT
	if v, ok := resources["utf8"]; ok {
		text = string(v)
	} else if v, ok := resources["TEXT"]; ok {
		text = string(v)
	}
	if v, ok := resources["HTML"]; ok {
		html = string(v)
	}
	if v, ok := resources["utxt"]; ok && text == "" {
		text = decodeUTF16(v)
	}
	return
}

func decodeUTF16(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	// Handle BOM
	bigEndian := true
	if b[0] == 0xFE && b[1] == 0xFF {
		b = b[2:]
	} else if b[0] == 0xFF && b[1] == 0xFE {
		bigEndian = false
		b = b[2:]
	}

	runes := make([]rune, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		var v uint16
		if bigEndian {
			v = binary.BigEndian.Uint16(b[i : i+2])
		} else {
			v = binary.LittleEndian.Uint16(b[i : i+2])
		}
		runes = append(runes, rune(v))
	}
	return string(runes)
}

func stripHTML(html string) string {
	var buf strings.Builder
	inTag := false
	for _, r := range html {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				buf.WriteRune(r)
			}
		}
	}
	return buf.String()
}

// pickString extracts a string value from a plist dict, handling
// both <string> (Go string) and <data> (Go []byte, UTF-16).
func pickString(dict map[string]interface{}, key string) string {
	switch v := dict[key].(type) {
	case string:
		return v
	case []byte:
		// plist <data> — check if it's UTF-16 (every other byte is 0 for ASCII)
		if len(v) >= 2 && (v[0] == 0xFE || v[0] == 0xFF || (len(v) > 2 && v[1] == 0)) {
			return decodeUTF16(v)
		}
		return string(v)
	default:
		return ""
	}
}

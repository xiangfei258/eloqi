package windows

import (
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf16"
)

func encodeWindowsClipboardText(text string) ([]byte, error) {
	if strings.ContainsRune(text, '\x00') {
		return nil, fmt.Errorf("windows clipboard: text contains an embedded NUL")
	}
	units := utf16.Encode([]rune(text))
	units = append(units, 0)
	bytes := make([]byte, len(units)*2)
	for index, unit := range units {
		binary.LittleEndian.PutUint16(bytes[index*2:], unit)
	}
	return bytes, nil
}

func decodeWindowsClipboardText(bytes []byte) (string, error) {
	if len(bytes)%2 != 0 {
		return "", fmt.Errorf("windows clipboard: CF_UNICODETEXT has odd byte length %d", len(bytes))
	}
	units := make([]uint16, len(bytes)/2)
	for index := range units {
		units[index] = binary.LittleEndian.Uint16(bytes[index*2:])
	}
	end := 0
	for end < len(units) && units[end] != 0 {
		end++
	}
	return string(utf16.Decode(units[:end])), nil
}

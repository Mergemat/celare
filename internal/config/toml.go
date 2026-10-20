package config

import (
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
)

// quote renders s as a TOML basic string.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// decodeValue decodes a single TOML value literal into v.V.
func decodeValue(literal string, v any) (toml.MetaData, error) {
	return toml.Decode("V = "+literal, v)
}

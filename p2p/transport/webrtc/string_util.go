package libp2pwebrtc

import "strings"

func maFingerprintToSdp(fp string) string {
	result := ""
	first := true
	for pos, char := range fp {
		if pos%2 == 0 {
			if first {
				first = false
			} else {
				result += ":"
			}
		}
		result += string(char)
	}
	return result
}

// intersperse string s with rune c at intervals of
// length k
func intersperse(s string, c rune, k int) string {
	builder := &strings.Builder{}
	builder.Grow(len(s) + len(s)/k)
	for pos, ch := range s {
		if pos%k == 0 && pos > 0 {
			builder.WriteRune(c)
		}
		builder.WriteRune(ch)
	}
	return builder.String()
}

// assumption: all runes are ASCII
func intersperse2(s string, c byte, k int) string {
	m := len(s)
	n := 0
	buf := make([]byte, m+m/k)
	for i, b := range []byte(s) {
		if i%k == 0 && i > 0 {
			buf[n] = c
			n++
		}
		buf[n] = b
		n++
	}
	return string(buf[:n])
}

func removeAll(s string, b byte) string {
	buf := make([]byte, len(s))
	k := 0
	for _, c := range []byte(s) {
		if c != b {
			buf[k] = c
			k++
		}
	}
	return string(buf[:k])
}

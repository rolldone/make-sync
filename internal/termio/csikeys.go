package termio

import (
	"bytes"
	"strconv"
)

// CSISeq holds a parsed CSI (Control Sequence Introducer) numeric token list.
// The raw sequence for CSI is the bytes between '[' and terminating '_' (exclusive).
// Tokens is the list of numeric tokens parsed from that area.
type CSISeq struct {
	Tokens []int
	Raw    []byte
}

// ParseCSI attempts to find a CSI block (ESC '[' ... '_') inside data and returns
// the parsed numeric tokens and the raw bytes of the sequence. If not found, ok=false.
func ParseCSI(data []byte) (seq CSISeq, ok bool) {
	start := bytes.Index(data, []byte{0x1b, '['})
	if start < 0 {
		return CSISeq{}, false
	}
	rest := data[start+2:]
	end := bytes.IndexByte(rest, '_')
	if end < 0 {
		return CSISeq{}, false
	}
	raw := rest[:end]
	parts := bytes.Split(raw, []byte{';'})
	var toks []int
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		n, err := strconv.Atoi(string(p))
		if err != nil {
			continue
		}
		toks = append(toks, n)
	}
	return CSISeq{Tokens: toks, Raw: raw}, true
}

// FindCSIAltDigit attempts to detect an Alt+digit inside the data's CSI block.
// It returns the digit (0-9) and true if found. The detection rules are:
//   - If tokens contain an ASCII digit value (48..57), return that mapped digit.
//   - If tokens contain a modifier token value of 2 (common Alt marker) AND the
//     last numeric token is between 0..9 then return that last token.
//   - Otherwise return false.
func FindCSIAltDigit(data []byte) (int, bool) {
	seq, ok := ParseCSI(data)
	if !ok {
		return 0, false
	}
	// Conservative: require explicit modifier token (2) to consider this
	// sequence an Alt+digit. This avoids false positives where ASCII-digit
	// values appear in other non-modifier contexts.
	hasAlt := false
	for _, n := range seq.Tokens {
		if n == 2 {
			hasAlt = true
			break
		}
	}
	if !hasAlt || len(seq.Tokens) == 0 {
		return 0, false
	}
	// If there's an ASCII digit token prefer that.
	for _, n := range seq.Tokens {
		if n >= 48 && n <= 57 {
			return n - 48, true
		}
	}
	// fallback: if last token is small digit, accept it
	last := seq.Tokens[len(seq.Tokens)-1]
	if last >= 0 && last <= 9 {
		return last, true
	}
	return 0, false
}

// FindCSIAltDigitHeuristic applies conservative heuristics to decide whether a
// CSI sequence likely represents Alt+digit even when an explicit modifier is
// missing. It returns (digit, true) only when heuristics indicate Alt; otherwise
// returns (0, false).
func FindCSIAltDigitHeuristic(data []byte) (int, bool) {
	seq, ok := ParseCSI(data)
	if !ok {
		return 0, false
	}

	var asciiCount int
	var asciiDigit int = -1
	var hasModifier2 bool
	for _, n := range seq.Tokens {
		if n == 2 {
			hasModifier2 = true
		}
		if n >= 48 && n <= 57 {
			asciiCount++
			asciiDigit = n - 48
		}
	}

	// Rule 1: explicit modifier -> alt
	if hasModifier2 {
		if asciiDigit >= 0 {
			return asciiDigit, true
		}
		// fallback to last small token
		if len(seq.Tokens) > 0 {
			last := seq.Tokens[len(seq.Tokens)-1]
			if last >= 0 && last <= 9 {
				return last, true
			}
		}
		return 0, false
	}

	// Rule 2: ASCII digit appears multiple times in tokens -> likely Alt
	if asciiCount >= 2 && asciiDigit >= 0 {
		return asciiDigit, true
	}

	// Rule 3: single ASCII digit without modifier -> treat as plain digit (not Alt)
	// Conservative: do not mark as Alt to avoid false positives.
	return 0, false
}

// FindExactCSIAltPattern checks parsed CSI numeric tokens against known
// exact token sequences observed on some terminals. It returns:
//
//	(digit, altAlone, true) when a known exact pattern is matched.
//
// Examples (token sequences are decimal numeric tokens parsed from the CSI
// block):
//
//	alt alone  => 18 56 0 1 2 1
//	alt + 8    => 56 9 56 0 2 1
//
// These patterns were provided by the user as observed token lists; matching
// them exactly helps avoid heuristic mistakes. If no exact match is found,
// (0,false,false) is returned.
func BlockALTOnly(data []byte) (int, bool, bool) {
	seq, ok := ParseCSI(data)
	if !ok {
		return 0, false, false
	}
	// Known exact patterns (as provided by user).
	altAlone := []int{18, 56, 0, 1, 2, 1}
	alt8 := []int{56, 9, 56, 0, 2, 1}

	matches := func(a, b []int) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	if matches(seq.Tokens, altAlone) {
		return 0, true, true
	}
	if matches(seq.Tokens, alt8) {
		return 8, false, true
	}
	return 0, false, false
}

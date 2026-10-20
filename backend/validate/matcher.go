package validate

import (
	"strings"
	"unicode"

	"github.com/eeyung1/listenledger-backend/models"
)

// MatchResult holds the outcome of resolving a candidate product against the catalogue.
type MatchResult struct {
	Product    *models.Product
	Confidence float64
	Ambiguous  bool
	MatchedBy  string // "sku", "name", "alias", "phonetic", "fuzzy", "none"
}

// MatchProduct searches a shop's catalogue for a candidate product mention.
// Precedence:
//  1. Exact SKU match (1.0)
//  2. Exact product name match (0.98)
//  3. Exact alias match (0.95)
//  4. Phonetic match on name/aliases via Double Metaphone (0.88–0.92)
//  5. Conservative fuzzy match on name/aliases (0.80–0.90)
//  6. Ambiguous or unknown (0.0–0.50)
func MatchProduct(candidate string, catalogue []models.Product) MatchResult {
	norm := normalizeText(candidate)
	if norm == "" || len(catalogue) == 0 {
		return MatchResult{Confidence: 0.0, MatchedBy: "none"}
	}

	// Filter out archived products: soft-deactivated products are preserved in the ledger
	// but are never matched against active speech recognition.
	var active []models.Product
	for _, p := range catalogue {
		if !p.IsArchived() {
			active = append(active, p)
		}
	}
	catalogue = active
	if len(catalogue) == 0 {
		return MatchResult{Confidence: 0.0, MatchedBy: "none"}
	}

	// 1. Exact SKU match
	for i := range catalogue {
		p := &catalogue[i]
		if strings.EqualFold(strings.TrimSpace(p.SKU), strings.TrimSpace(candidate)) || normalizeText(p.SKU) == norm {
			return MatchResult{
				Product:    p,
				Confidence: 1.0,
				MatchedBy:  "sku",
			}
		}
	}

	// 2. Exact Name match
	for i := range catalogue {
		p := &catalogue[i]
		if normalizeText(p.Name) == norm {
			return MatchResult{
				Product:    p,
				Confidence: 0.98,
				MatchedBy:  "name",
			}
		}
	}

	// 3. Exact Alias match
	var aliasMatches []*models.Product
	for i := range catalogue {
		p := &catalogue[i]
		for _, alias := range p.Aliases {
			if normalizeText(alias) == norm {
				aliasMatches = append(aliasMatches, p)
				break
			}
		}
	}
	if len(aliasMatches) == 1 {
		return MatchResult{
			Product:    aliasMatches[0],
			Confidence: 0.95,
			MatchedBy:  "alias",
		}
	} else if len(aliasMatches) > 1 {
		// Ambiguous match across different products with identical alias
		return MatchResult{
			Product:    aliasMatches[0],
			Confidence: 0.50,
			Ambiguous:  true,
			MatchedBy:  "alias",
		}
	}

	// 4. Phonetic match on name/aliases via Double Metaphone
	if res, ok := matchPhoneticProduct(candidate, catalogue); ok {
		return res
	}

	// 5. Conservative fuzzy match
	type fuzzyScore struct {
		product *models.Product
		score   float64
	}
	var scores []fuzzyScore

	for i := range catalogue {
		p := &catalogue[i]
		bestPScore := similarity(norm, normalizeText(p.Name))
		for _, alias := range p.Aliases {
			s := similarity(norm, normalizeText(alias))
			if s > bestPScore {
				bestPScore = s
			}
		}
		if bestPScore >= 0.75 {
			scores = append(scores, fuzzyScore{product: p, score: bestPScore})
		}
	}

	if len(scores) == 0 {
		return MatchResult{Confidence: 0.0, MatchedBy: "none"}
	}

	// Sort highest score first
	for i := 0; i < len(scores)-1; i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].score > scores[i].score {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}

	best := scores[0]
	if len(scores) > 1 && (best.score-scores[1].score) < 0.10 && scores[1].score >= 0.75 {
		// Two catalogue items match closely: ambiguous!
		return MatchResult{
			Product:    best.product,
			Confidence: 0.50,
			Ambiguous:  true,
			MatchedBy:  "fuzzy",
		}
	}

	// Clean single fuzzy match
	conf := 0.80 + (best.score-0.75)*0.40 // maps 0.75..1.0 to 0.80..0.90
	if conf > 0.90 {
		conf = 0.90
	}
	return MatchResult{
		Product:    best.product,
		Confidence: conf,
		MatchedBy:  "fuzzy",
	}
}

// normalizeText strips punctuation, lowers case, and collapses whitespace.
func normalizeText(s string) string {
	var b strings.Builder
	lastSpace := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastSpace = false
		} else if unicode.IsSpace(r) || r == '-' || r == '/' {
			if !lastSpace && b.Len() > 0 {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// similarity computes a combined token overlap and Levenshtein similarity ratio [0, 1].
func similarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if a == "" || b == "" {
		return 0.0
	}

	// 1. Check substring/token containment
	if strings.Contains(b, a) || strings.Contains(a, b) {
		shortLen := float64(min(len(a), len(b)))
		longLen := float64(max(len(a), len(b)))
		return 0.75 + 0.25*(shortLen/longLen)
	}

	// 2. Levenshtein distance ratio
	dist := levenshteinDistance(a, b)
	maxLen := max(len(a), len(b))
	ratio := 1.0 - (float64(dist) / float64(maxLen))
	if ratio < 0 {
		return 0
	}
	return ratio
}

func levenshteinDistance(s, t string) int {
	d := make([][]int, len(s)+1)
	for i := range d {
		d[i] = make([]int, len(t)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(s); i++ {
		for j := 1; j <= len(t); j++ {
			cost := 1
			if s[i-1] == t[j-1] {
				cost = 0
			}
			d[i][j] = min(
				d[i-1][j]+1,
				d[i][j-1]+1,
				d[i-1][j-1]+cost,
			)
		}
	}
	return d[len(s)][len(t)]
}

// CustomerMatchResult holds the outcome of resolving a candidate customer mention.
type CustomerMatchResult struct {
	Customer   *models.Customer
	Confidence float64
	Ambiguous  bool
	MatchedBy  string // "id", "exact_name", "token", "phonetic", "fuzzy", "none"
}

// MatchCustomer searches a shop's registered customers for a candidate spoken name.
func MatchCustomer(candidate string, customers []models.Customer) CustomerMatchResult {
	norm := normalizeText(candidate)
	if norm == "" || len(customers) == 0 {
		return CustomerMatchResult{Confidence: 0.0, MatchedBy: "none"}
	}

	// 1. Exact ID match
	for i := range customers {
		c := &customers[i]
		if strings.EqualFold(strings.TrimSpace(c.ID), strings.TrimSpace(candidate)) {
			return CustomerMatchResult{
				Customer:   c,
				Confidence: 1.0,
				MatchedBy:  "id",
			}
		}
	}

	// 2. Exact Name match
	for i := range customers {
		c := &customers[i]
		if normalizeText(c.Name) == norm {
			return CustomerMatchResult{
				Customer:   c,
				Confidence: 0.98,
				MatchedBy:  "exact_name",
			}
		}
	}

	// 3. Token containment (e.g. spoken "Emeka" matches customer "Emeka Okafor")
	var tokenMatches []*models.Customer
	candWords := strings.Fields(norm)
	for i := range customers {
		c := &customers[i]
		cNorm := normalizeText(c.Name)
		cWords := strings.Fields(cNorm)

		allMatched := true
		for _, cw := range candWords {
			wordFound := false
			for _, w := range cWords {
				if w == cw {
					wordFound = true
					break
				}
			}
			if !wordFound {
				allMatched = false
				break
			}
		}
		if allMatched {
			tokenMatches = append(tokenMatches, c)
		}
	}

	if len(tokenMatches) == 1 {
		return CustomerMatchResult{
			Customer:   tokenMatches[0],
			Confidence: 0.92,
			MatchedBy:  "token",
		}
	} else if len(tokenMatches) > 1 {
		return CustomerMatchResult{
			Customer:   tokenMatches[0],
			Confidence: 0.50,
			Ambiguous:  true,
			MatchedBy:  "token",
		}
	}

	// 4. Phonetic match on customer names
	if res, ok := matchPhoneticCustomer(candidate, customers); ok {
		return res
	}

	// 5. Conservative fuzzy match
	type custFuzzyScore struct {
		customer *models.Customer
		score    float64
	}
	var scores []custFuzzyScore
	for i := range customers {
		c := &customers[i]
		s := similarity(norm, normalizeText(c.Name))
		if s >= 0.75 {
			scores = append(scores, custFuzzyScore{customer: c, score: s})
		}
	}

	if len(scores) == 0 {
		return CustomerMatchResult{Confidence: 0.0, MatchedBy: "none"}
	}

	for i := 0; i < len(scores)-1; i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].score > scores[i].score {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}

	best := scores[0]
	if len(scores) > 1 && (best.score-scores[1].score) < 0.10 && scores[1].score >= 0.75 {
		return CustomerMatchResult{
			Customer:   best.customer,
			Confidence: 0.50,
			Ambiguous:  true,
			MatchedBy:  "fuzzy",
		}
	}

	conf := 0.80 + (best.score-0.75)*0.40
	if conf > 0.90 {
		conf = 0.90
	}
	return CustomerMatchResult{
		Customer:   best.customer,
		Confidence: conf,
		MatchedBy:  "fuzzy",
	}
}

type phoneticToken struct {
	raw       string
	primary   string
	secondary string
}

func tokenizePhonetic(s string) []phoneticToken {
	fields := strings.Fields(s)
	tokens := make([]phoneticToken, 0, len(fields))
	for _, f := range fields {
		clean := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return unicode.ToLower(r)
			}
			return -1
		}, f)
		if clean == "" {
			continue
		}
		p, sec := DoubleMetaphone(clean)
		tokens = append(tokens, phoneticToken{
			raw:       clean,
			primary:   p,
			secondary: sec,
		})
	}
	return tokens
}

func tokenPhoneticMatch(a, b phoneticToken) bool {
	if a.raw == b.raw {
		return true
	}
	if a.primary != "" && b.primary != "" {
		if a.primary == b.primary || (b.secondary != "" && a.primary == b.secondary) {
			return true
		}
		if a.secondary != "" && (a.secondary == b.primary || (b.secondary != "" && a.secondary == b.secondary)) {
			return true
		}
	}
	return false
}

func matchPhoneticTokens(candTokens, targetTokens []phoneticToken) float64 {
	m := len(candTokens)
	n := len(targetTokens)
	if m == 0 || n == 0 {
		return 0.0
	}

	// 1. Exact token length match
	if m == n {
		allMatch := true
		for i := 0; i < m; i++ {
			if !tokenPhoneticMatch(candTokens[i], targetTokens[i]) {
				allMatch = false
				break
			}
		}
		if allMatch {
			return 0.92
		}
	}

	// 2. Candidate matches prefix of target (e.g. "peek" matches "Peak Milk 400g")
	if m < n {
		prefixMatch := true
		for i := 0; i < m; i++ {
			if !tokenPhoneticMatch(candTokens[i], targetTokens[i]) {
				prefixMatch = false
				break
			}
		}
		if prefixMatch {
			if m >= 2 {
				return 0.90
			}
			return 0.88
		}

		// 3. Subsequence match within target
		for start := 1; start <= n-m; start++ {
			subMatch := true
			for i := 0; i < m; i++ {
				if !tokenPhoneticMatch(candTokens[i], targetTokens[start+i]) {
					subMatch = false
					break
				}
			}
			if subMatch {
				return 0.88
			}
		}
	}

	return 0.0
}

func matchPhoneticProduct(candidate string, catalogue []models.Product) (MatchResult, bool) {
	candTokens := tokenizePhonetic(candidate)
	if len(candTokens) == 0 || len(catalogue) == 0 {
		return MatchResult{}, false
	}

	type candScore struct {
		product *models.Product
		score   float64
	}
	var scores []candScore

	for i := range catalogue {
		p := &catalogue[i]
		bestScore := matchPhoneticTokens(candTokens, tokenizePhonetic(p.Name))
		for _, alias := range p.Aliases {
			s := matchPhoneticTokens(candTokens, tokenizePhonetic(alias))
			if s > bestScore {
				bestScore = s
			}
		}
		if bestScore >= 0.88 {
			scores = append(scores, candScore{product: p, score: bestScore})
		}
	}

	if len(scores) == 0 {
		return MatchResult{}, false
	}

	// Sort highest score first
	for i := 0; i < len(scores)-1; i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].score > scores[i].score {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}

	best := scores[0]
	// Check for ambiguous collision between different products
	if len(scores) > 1 && (best.score-scores[1].score) < 0.05 && scores[1].score >= 0.88 {
		return MatchResult{
			Product:    best.product,
			Confidence: 0.50,
			Ambiguous:  true,
			MatchedBy:  "phonetic",
		}, true
	}

	return MatchResult{
		Product:    best.product,
		Confidence: best.score,
		MatchedBy:  "phonetic",
	}, true
}

func matchPhoneticCustomer(candidate string, customers []models.Customer) (CustomerMatchResult, bool) {
	candTokens := tokenizePhonetic(candidate)
	if len(candTokens) == 0 || len(customers) == 0 {
		return CustomerMatchResult{}, false
	}

	type custScore struct {
		customer *models.Customer
		score    float64
	}
	var scores []custScore

	for i := range customers {
		c := &customers[i]
		s := matchPhoneticTokens(candTokens, tokenizePhonetic(c.Name))
		if s >= 0.88 {
			scores = append(scores, custScore{customer: c, score: s})
		}
	}

	if len(scores) == 0 {
		return CustomerMatchResult{}, false
	}

	for i := 0; i < len(scores)-1; i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[j].score > scores[i].score {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}

	best := scores[0]
	if len(scores) > 1 && (best.score-scores[1].score) < 0.05 && scores[1].score >= 0.88 {
		return CustomerMatchResult{
			Customer:   best.customer,
			Confidence: 0.50,
			Ambiguous:  true,
			MatchedBy:  "phonetic",
		}, true
	}

	return CustomerMatchResult{
		Customer:   best.customer,
		Confidence: best.score,
		MatchedBy:  "phonetic",
	}, true
}

// DoubleMetaphone returns the primary and secondary Double Metaphone phonetic codes for a word.
// It encodes phonetic pronunciation rules based on Lawrence Philips' Double Metaphone algorithm.
func DoubleMetaphone(s string) (string, string) {
	var clean strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			clean.WriteRune(r)
		}
	}
	word := clean.String()
	if word == "" {
		return "", ""
	}

	length := len(word)
	var primary, secondary strings.Builder
	maxLen := 4

	add := func(p string, s string) {
		if p != "" && primary.Len() < maxLen {
			primary.WriteString(p)
		}
		if s != "" && secondary.Len() < maxLen {
			secondary.WriteString(s)
		}
	}

	addChar := func(p byte, s byte) {
		if p != 0 && primary.Len() < maxLen {
			primary.WriteByte(p)
		}
		if s != 0 && secondary.Len() < maxLen {
			secondary.WriteByte(s)
		}
	}

	addSame := func(ch byte) {
		addChar(ch, ch)
	}

	isVowel := func(idx int) bool {
		if idx < 0 || idx >= length {
			return false
		}
		b := word[idx]
		return b == 'A' || b == 'E' || b == 'I' || b == 'O' || b == 'U' || b == 'Y'
	}

	charAt := func(idx int) byte {
		if idx < 0 || idx >= length {
			return 0
		}
		return word[idx]
	}

	stringAt := func(start, length int, targets ...string) bool {
		if start < 0 || start+length > len(word) {
			return false
		}
		sub := word[start : start+length]
		for _, target := range targets {
			if sub == target {
				return true
			}
		}
		return false
	}

	pos := 0

	if stringAt(0, 2, "GN", "KN", "PN", "WR", "PS") {
		pos = 1
	} else if charAt(0) == 'X' {
		addSame('S')
		pos = 1
	}

	for pos < length && (primary.Len() < maxLen || secondary.Len() < maxLen) {
		ch := word[pos]

		if unicode.IsDigit(rune(ch)) {
			addSame(ch)
			pos++
			continue
		}

		switch ch {
		case 'A', 'E', 'I', 'O', 'U', 'Y':
			if pos == 0 {
				addSame('A')
			}
			pos++

		case 'B':
			addSame('P')
			if charAt(pos+1) == 'B' {
				pos += 2
			} else {
				pos++
			}

		case 'C':
			if pos > 1 && !isVowel(pos-2) && stringAt(pos-1, 3, "ACH") && charAt(pos+2) != 'I' && (charAt(pos+2) != 'E' || stringAt(pos-2, 6, "BACHER", "MACHER")) {
				addSame('K')
				pos += 2
			} else if pos == 0 && stringAt(pos, 6, "CAESAR") {
				addSame('S')
				pos += 2
			} else if stringAt(pos, 2, "CH") {
				if pos > 0 && stringAt(pos, 4, "CHAE") {
					addChar('K', 'X')
					pos += 2
				} else if pos == 0 && (stringAt(pos+1, 5, "HARAC", "HARIS") || stringAt(pos+1, 3, "HOR", "HYM", "HIA", "HEM")) && !stringAt(0, 5, "CHORE") {
					addSame('K')
					pos += 2
				} else if stringAt(0, 4, "VAN ", "VON ") || stringAt(0, 3, "SCH") || stringAt(pos-2, 6, "ORCHES", "ARCHIT", "ORCHID") || stringAt(pos+2, 1, "T", "S") || ((pos == 0 || stringAt(pos-1, 1, "A", "O", "U", "E")) && stringAt(pos+2, 1, "L", "R", "N", "M", "B", "H", "F", "V", "W", " ")) {
					addSame('K')
					pos += 2
				} else {
					if pos > 0 {
						if stringAt(0, 2, "MC") {
							addSame('K')
						} else {
							addChar('X', 'K')
						}
					} else {
						addSame('X')
					}
					pos += 2
				}
			} else if stringAt(pos, 2, "CZ") && !stringAt(pos-2, 4, "WICZ") {
				addChar('S', 'X')
				pos += 2
			} else if stringAt(pos+1, 2, "IA") {
				addSame('S')
				pos += 3
			} else if stringAt(pos, 2, "CC") && !(pos == 1 && charAt(0) == 'M') {
				if stringAt(pos+2, 1, "I", "E", "H") && !stringAt(pos+2, 2, "HU") {
					if (pos == 1 && charAt(pos-1) == 'A') || stringAt(pos-1, 5, "UCCEE", "UCCES") {
						add("KS", "KS")
					} else {
						add("X", "KS")
					}
					pos += 3
				} else {
					addSame('K')
					pos += 2
				}
			} else if stringAt(pos, 2, "CK", "CG", "CQ") {
				addSame('K')
				pos += 2
			} else if stringAt(pos, 2, "CI", "CE", "CY") {
				if stringAt(pos, 3, "CIO", "CIE", "CIA") {
					addChar('S', 'X')
				} else {
					addSame('S')
				}
				pos += 2
			} else {
				addSame('K')
				if stringAt(pos+1, 2, " C", " Q", " G") {
					pos += 3
				} else if stringAt(pos+1, 1, "C", "K", "Q") && !stringAt(pos+1, 2, "CE", "CI") {
					pos += 2
				} else {
					pos++
				}
			}

		case 'D':
			if stringAt(pos, 2, "DG") {
				if stringAt(pos+2, 1, "I", "E", "Y") {
					addSame('J')
					pos += 3
				} else {
					add("TK", "TK")
					pos += 2
				}
			} else if stringAt(pos, 2, "DT", "DD") {
				addSame('T')
				pos += 2
			} else {
				addSame('T')
				pos++
			}

		case 'F':
			addSame('F')
			if charAt(pos+1) == 'F' {
				pos += 2
			} else {
				pos++
			}

		case 'G':
			if charAt(pos+1) == 'H' {
				if pos > 0 && !isVowel(pos-1) {
					addSame('K')
					pos += 2
				} else if pos == 0 {
					if charAt(pos+2) == 'I' {
						addSame('J')
					} else {
						addSame('K')
					}
					pos += 2
				} else if (pos > 1 && stringAt(pos-2, 1, "B", "H", "D")) || (pos > 2 && stringAt(pos-3, 1, "B", "H", "D")) || (pos > 3 && stringAt(pos-4, 1, "B", "H")) {
					pos += 2
				} else {
					if pos > 2 && charAt(pos-1) == 'U' && stringAt(pos-3, 1, "C", "G", "L", "R", "T") {
						addSame('F')
					} else if pos > 0 && charAt(pos-1) != 'I' {
						addSame('K')
					}
					pos += 2
				}
			} else if charAt(pos+1) == 'N' {
				if pos == 1 && isVowel(0) && !stringAt(0, 6, "DANGER", "MANGER", "GANGER") {
					addChar('K', 'J')
				} else if !stringAt(pos+2, 2, "EY") && charAt(pos+1) != 'Y' && !stringAt(0, 6, "DANGER", "MANGER", "GANGER") {
					addChar('N', 'K')
				} else {
					addSame('K')
				}
				pos += 2
			} else if stringAt(pos+1, 2, "LI") {
				addChar('L', 'J')
				pos += 2
			} else if pos == 0 && (charAt(pos+1) == 'Y' || stringAt(pos+1, 2, "ES", "EP", "EB", "EL", "EY", "IB", "IL", "IN", "IE", "EI", "ER")) {
				addChar('K', 'J')
				pos += 2
			} else if (stringAt(pos+1, 2, "ER") || charAt(pos+1) == 'Y') && !stringAt(0, 6, "DANGER", "RANGER", "MANGER") && !stringAt(pos-1, 1, "E", "I") && !stringAt(pos-1, 3, "RGY", "OGY") {
				addChar('K', 'J')
				pos += 2
			} else if stringAt(pos+1, 1, "E", "I", "Y") || stringAt(pos-1, 4, "AGGI", "OGGI") {
				if stringAt(0, 4, "VAN ", "VON ") || stringAt(0, 3, "SCH") || stringAt(pos+1, 2, "ET") {
					addSame('K')
				} else {
					addChar('J', 'K')
				}
				pos += 2
			} else {
				addSame('K')
				if charAt(pos+1) == 'G' {
					pos += 2
				} else {
					pos++
				}
			}

		case 'H':
			if (pos == 0 || isVowel(pos-1)) && isVowel(pos+1) {
				addSame('H')
				pos += 2
			} else {
				pos++
			}

		case 'J':
			if stringAt(pos, 4, "JOSE", "SAN ") || stringAt(0, 4, "SAN ") {
				if (pos == 0 && charAt(pos+4) == ' ') || stringAt(0, 4, "SAN ") {
					addSame('H')
				} else {
					addChar('J', 'H')
				}
				pos++
			} else {
				if pos == 0 && !stringAt(pos, 4, "JOSE") {
					addChar('J', 'A')
				} else if isVowel(pos-1) && !stringAt(0, 4, "SAN ") && (charAt(pos+1) == 'A' || charAt(pos+1) == 'O') {
					addChar('J', 'H')
				} else if pos == length-1 {
					addChar('J', 0)
				} else if !stringAt(pos+1, 1, "L", "T", "K", "S", "N", "M", "B", "Z") && !stringAt(pos-1, 1, "S", "K", "L") {
					addSame('J')
				}
				if charAt(pos+1) == 'J' {
					pos += 2
				} else {
					pos++
				}
			}

		case 'K':
			addSame('K')
			if charAt(pos+1) == 'K' {
				pos += 2
			} else {
				pos++
			}

		case 'L':
			addSame('L')
			if charAt(pos+1) == 'L' {
				pos += 2
			} else {
				pos++
			}

		case 'M':
			if (stringAt(pos-1, 3, "UMB") && (pos+1 == length-1 || stringAt(pos+2, 2, "ER"))) || charAt(pos+1) == 'B' {
				// silent b in thumb, etc
			}
			addSame('M')
			if charAt(pos+1) == 'M' {
				pos += 2
			} else {
				pos++
			}

		case 'N':
			addSame('N')
			if charAt(pos+1) == 'N' {
				pos += 2
			} else {
				pos++
			}

		case 'P':
			if charAt(pos+1) == 'H' {
				addSame('F')
				pos += 2
			} else {
				addSame('P')
				if stringAt(pos+1, 1, "P", "B") {
					pos += 2
				} else {
					pos++
				}
			}

		case 'Q':
			addSame('K')
			if charAt(pos+1) == 'Q' {
				pos += 2
			} else {
				pos++
			}

		case 'R':
			addSame('R')
			if charAt(pos+1) == 'R' {
				pos += 2
			} else {
				pos++
			}

		case 'S':
			if stringAt(pos-1, 3, "ISL", "YSL") {
				pos++
			} else if pos == 0 && stringAt(pos, 5, "SUGAR") {
				addChar('X', 'S')
				pos++
			} else if stringAt(pos, 2, "SH") {
				if stringAt(pos+1, 4, "HEIM", "HOEK", "HOLM", "HOLZ") {
					addSame('S')
				} else {
					addSame('X')
				}
				pos += 2
			} else if stringAt(pos, 3, "SIO", "SIA") || stringAt(pos, 4, "SIAN") {
				addChar('S', 'X')
				pos += 3
			} else if (pos == 0 && stringAt(pos+1, 1, "M", "N", "L", "W")) || stringAt(pos+1, 1, "Z") {
				addChar('S', 'X')
				if charAt(pos+1) == 'Z' {
					pos += 2
				} else {
					pos++
				}
			} else if stringAt(pos, 2, "SC") {
				if charAt(pos+2) == 'H' {
					if stringAt(pos+3, 2, "OO", "ER", "EN", "UY", "ED", "EM") {
						if stringAt(pos+3, 2, "ER", "EN") {
							add("X", "SK")
						} else {
							addSame('S')
							addSame('K')
						}
						pos += 3
					} else {
						if pos == 0 && !isVowel(3) && charAt(3) != 'W' {
							addChar('X', 'S')
						} else {
							addSame('X')
						}
						pos += 3
					}
				} else if stringAt(pos+2, 1, "I", "E", "Y") {
					addSame('S')
					pos += 3
				} else {
					addSame('S')
					addSame('K')
					pos += 3
				}
			} else {
				addSame('S')
				if stringAt(pos+1, 1, "S", "Z") {
					pos += 2
				} else {
					pos++
				}
			}

		case 'T':
			if stringAt(pos, 4, "TION") || stringAt(pos, 3, "TIA", "TCH") {
				addSame('X')
				pos += 3
			} else if stringAt(pos, 2, "TH") || stringAt(pos, 3, "TTH") {
				addChar('0', 'T')
				pos += 2
			} else {
				addSame('T')
				if stringAt(pos+1, 1, "T", "D") {
					pos += 2
				} else {
					pos++
				}
			}

		case 'V':
			addSame('F')
			if charAt(pos+1) == 'V' {
				pos += 2
			} else {
				pos++
			}

		case 'W':
			if stringAt(pos, 2, "WR") {
				addSame('R')
				pos += 2
			} else if pos == 0 && (isVowel(pos+1) || stringAt(pos, 2, "WH")) {
				if isVowel(pos + 1) {
					addChar('A', 'F')
				} else {
					addSame('A')
				}
				pos++
			} else if (pos == length-1 && isVowel(pos-1)) || stringAt(pos-1, 5, "EWSKI", "EWSKY", "OWSKI", "OWSKY") || stringAt(0, 3, "SCH") {
				addChar(0, 'F')
				pos++
			} else {
				pos++
			}

		case 'X':
			if !(pos == length-1 && (stringAt(pos-3, 3, "IAU", "EAU") || stringAt(pos-2, 2, "AU", "OU"))) {
				add("KS", "KS")
			}
			if stringAt(pos+1, 1, "C", "X") {
				pos += 2
			} else {
				pos++
			}

		case 'Z':
			if charAt(pos+1) == 'H' {
				addSame('J')
				pos += 2
			} else {
				addSame('S')
				if charAt(pos+1) == 'Z' {
					pos += 2
				} else {
					pos++
				}
			}

		default:
			pos++
		}
	}

	pStr := primary.String()
	sStr := secondary.String()
	if len(pStr) > maxLen {
		pStr = pStr[:maxLen]
	}
	if len(sStr) > maxLen {
		sStr = sStr[:maxLen]
	}
	if pStr == sStr {
		sStr = ""
	}
	return pStr, sStr
}

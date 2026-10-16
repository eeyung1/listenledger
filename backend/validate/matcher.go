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
	MatchedBy  string // "sku", "name", "alias", "fuzzy", "none"
}

// MatchProduct searches a shop's catalogue for a candidate product mention.
// Precedence:
//  1. Exact SKU match (1.0)
//  2. Exact product name match (0.98)
//  3. Exact alias match (0.95)
//  4. Conservative fuzzy match on name/aliases (0.80–0.90)
//  5. Ambiguous or unknown (0.0–0.50)
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

	// 4. Conservative fuzzy match
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
	MatchedBy  string // "id", "exact_name", "token", "fuzzy", "none"
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

	// 4. Conservative fuzzy match
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

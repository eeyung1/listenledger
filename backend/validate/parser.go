package validate

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/eeyung1/listenledger-backend/models"
)

var (
	// Number words mapping
	unitsMap = map[string]int64{
		"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5,
		"six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10,
		"eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15,
		"sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19,
		"twenty": 20, "thirty": 30, "forty": 40, "fifty": 50,
		"sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90,
		"dozen": 12,
	}

	scalesMap = map[string]int64{
		"hundred":  100,
		"thousand": 1000,
		"million":  1000000,
	}

	knownUnits = map[string]models.Unit{
		"carton":   models.Unit("carton"),
		"cartons":  models.Unit("carton"),
		"pack":     models.Unit("pack"),
		"packs":    models.Unit("pack"),
		"packet":   models.Unit("pack"),
		"packets":  models.Unit("pack"),
		"bottle":   models.Unit("bottle"),
		"bottles":  models.Unit("bottle"),
		"piece":    models.Unit("piece"),
		"pieces":   models.Unit("piece"),
		"tin":      models.Unit("tin"),
		"tins":     models.Unit("tin"),
		"crate":    models.Unit("crate"),
		"crates":   models.Unit("crate"),
		"bag":      models.Unit("bag"),
		"bags":     models.Unit("bag"),
		"sachet":   models.Unit("sachet"),
		"sachets":  models.Unit("sachet"),
		"roll":     models.Unit("roll"),
		"rolls":    models.Unit("roll"),
		"painter":  models.Unit("painter"),
		"painters": models.Unit("painter"),
		"paint":    models.Unit("painter"),
		"paints":   models.Unit("painter"),
		"mudu":     models.Unit("mudu"),
		"mudus":    models.Unit("mudu"),
		"mudo":     models.Unit("mudu"),
		"derica":   models.Unit("derica"),
		"dericas":  models.Unit("derica"),
		"derika":   models.Unit("derica"),
		"tier":     models.Unit("derica"),
		"tiers":    models.Unit("derica"),
		"rubber":   models.Unit("rubber"),
		"rubbers":  models.Unit("rubber"),
		"basin":    models.Unit("rubber"),
		"basins":   models.Unit("rubber"),
	}

	amountRegex = regexp.MustCompile(`(?i)\b(?:ngn\s*([0-9]+(?:,[0-9]{3})*(?:\.[0-9]+)?)|([0-9]+(?:,[0-9]{3})*(?:\.[0-9]+)?)\s*(?:naira|kobo|ngn)|([0-9]{1,3}(?:,[0-9]{3})+(?:\.[0-9]+)?))\b`)

	prefixKRegex        = regexp.MustCompile(`(?i)\bk[- ]?(fifty|forty|thirty|twenty|ten|five|hundred|[0-9]+)(?:\s*(?:naira|kobo|ngn))?\b`)
	suffixKRegex        = regexp.MustCompile(`(?i)\b([0-9]+(?:\.[0-9]+)?)\s*k(?:\s*(?:naira|kobo|ngn))?\b`)
	wordKRegex          = regexp.MustCompile(`(?i)\b((?:one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen|twenty|thirty|forty|fifty|hundred)(?:\s+(?:point\s+five|and\s+(?:a\s+)?half|[0-9]+))?)\s*k(?:\s*(?:naira|kobo|ngn))?\b`)
	shorthandFiveRegex  = regexp.MustCompile(`(?i)\b(one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen)[- ]five(?:\s*(?:naira|kobo|ngn))?\b`)
	shorthandFiftyRegex = regexp.MustCompile(`(?i)\b(one|two|three|four|five|six|seven|eight|nine|ten)[- ]fifty(?:\s*(?:naira|kobo|ngn))?\b`)

	prefixKWordMap = map[string]int64{
		"hundred": 100000,
		"fifty":   50000,
		"forty":   40000,
		"thirty":  30000,
		"twenty":  20000,
		"ten":     10000,
		"five":    5000,
	}
)

type unitMatcher struct {
	word string
	unit models.Unit
	re   *regexp.Regexp
}

// unitMatchers resolves unit words in a stable, sorted order. ParseSpokenSegment
// then prefers the earliest match in reading order, so a clause that names more
// than one unit no longer depends on Go map iteration order (P5.2).
var unitMatchers = func() []unitMatcher {
	words := make([]string, 0, len(knownUnits))
	for word := range knownUnits {
		words = append(words, word)
	}
	sort.Strings(words)
	matchers := make([]unitMatcher, 0, len(words))
	for _, word := range words {
		matchers = append(matchers, unitMatcher{
			word: word,
			unit: knownUnits[word],
			re:   regexp.MustCompile(`\b` + regexp.QuoteMeta(word) + `\b`),
		})
	}
	return matchers
}()

// ParsedAnchors contains raw tokens extracted from a spoken segment.
type ParsedAnchors struct {
	ProductMention string
	Quantity       int
	QuantityFound  bool
	// QuantityInferred is true when Quantity was defaulted to 1 because the
	// speaker named no quantity, rather than being heard (P5.5). Callers must
	// not treat an inferred quantity as spoken evidence.
	QuantityInferred bool
	Unit             models.Unit
	UnitFound        bool
	AmountMinor      int64
	AmountPhrase     string
	AmountFound      bool
	Currency         string
}

// ParseSpokenSegment extracts product mention, quantity, unit, and amount from one spoken clause.
func ParseSpokenSegment(text string, defaultCurrency string) ParsedAnchors {
	res := ParsedAnchors{Currency: defaultCurrency}
	if defaultCurrency == "" {
		res.Currency = "NGN"
	}

	cleaned := strings.TrimSpace(text)
	if cleaned == "" {
		return res
	}

	lower := strings.ToLower(cleaned)

	// 1. Extract Unit in reading order (not Go map order) so a clause that names
	// more than one unit resolves deterministically. The earliest unit word wins;
	// an equal position prefers the longer word, and unitMatchers is sorted
	// alphabetically to break any remaining tie.
	bestPos := -1
	bestWord := ""
	for _, m := range unitMatchers {
		loc := m.re.FindStringIndex(lower)
		if loc == nil {
			continue
		}
		if bestPos == -1 || loc[0] < bestPos || (loc[0] == bestPos && len(m.word) > len(bestWord)) {
			bestPos = loc[0]
			bestWord = m.word
			res.Unit = m.unit
			res.UnitFound = true
		}
	}

	// 2. Extract Amount
	if amt, phrase, ok := extractSpokenAmount(cleaned, lower); ok {
		res.AmountMinor = amt
		res.AmountPhrase = phrase
		res.AmountFound = true
	}

	// 3. Extract Quantity with self-correction handling
	if qty, ok, inferred := resolveSpokenQuantity(cleaned, lower, res); ok {
		res.Quantity = qty
		res.QuantityFound = true
		res.QuantityInferred = inferred
	}

	// 4. Extract Product Mention
	// Strip extracted numbers, units, amount phrases, fillers, and punctuation
	productText := cleanProductMention(cleaned, res)
	res.ProductMention = productText

	return res
}

// catalogueNamePattern builds a case-insensitive, word-bounded matcher for a
// catalogue product name or alias. Runs of non-alphanumeric characters become
// flexible separators so a catalogue "7Up" matches "7 up" and "Five Alive"
// matches "Five-Alive".
func catalogueNamePattern(name string) *regexp.Regexp {
	fields := regexp.MustCompile(`[^\p{L}\p{N}]+`).Split(strings.TrimSpace(name), -1)
	tokens := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			tokens = append(tokens, regexp.QuoteMeta(f))
		}
	}
	if len(tokens) == 0 {
		return nil
	}
	return regexp.MustCompile(`(?i)\b` + strings.Join(tokens, `[\s-]+`) + `\b`)
}

// ParseSpokenSegmentWithProducts parses one spoken clause, matching a known
// catalogue name or alias first and masking it out of the text before numbers
// are read. Number words inside brand names are otherwise mistaken for the
// quantity or the "<base>-five" price shorthand: "Two Five Alive, one thousand
// naira" parsed as product "Alive", quantity 1, ₦2,500 (the historical audit in
// README.md#history, roadmap P5.1). The longest matching name wins, so "Seven Up" beats
// "Up".
func ParseSpokenSegmentWithProducts(text, defaultCurrency string, productNames []string) ParsedAnchors {
	if len(productNames) == 0 {
		return ParseSpokenSegment(text, defaultCurrency)
	}

	// Longest name first, then alphabetical, so the result never depends on the
	// catalogue's order.
	candidates := append([]string(nil), productNames...)
	sort.SliceStable(candidates, func(i, j int) bool {
		if len(candidates[i]) != len(candidates[j]) {
			return len(candidates[i]) > len(candidates[j])
		}
		return candidates[i] < candidates[j]
	})

	for _, name := range candidates {
		pattern := catalogueNamePattern(name)
		if pattern == nil {
			continue
		}
		loc := pattern.FindStringIndex(text)
		if loc == nil {
			continue
		}

		// Replace the name with a neutral separator word instead of deleting it,
		// so a quantity word is not left adjacent to the amount phrase and summed
		// into it ("two Milo four thousand naira" must stay ₦4,000, not ₦6,000).
		masked := text[:loc[0]] + " item " + text[loc[1]:]
		res := ParseSpokenSegment(masked, defaultCurrency)
		res.ProductMention = strings.TrimSpace(name)

		if !res.UnitFound {
			// The catalogue name may itself contain a packaging word ("Pepsi
			// bottle"); keep the unit the unmodified clause would have found.
			if plain := ParseSpokenSegment(text, defaultCurrency); plain.UnitFound {
				res.Unit = plain.Unit
				res.UnitFound = true
			}
		}
		return res
	}

	return ParseSpokenSegment(text, defaultCurrency)
}

func parseNumberWords(str string) int64 {
	words := strings.Fields(strings.ReplaceAll(strings.ReplaceAll(str, ",", " "), "-", " "))
	var current int64
	var total int64
	found := false
	for _, w := range words {
		if val, ok := unitsMap[w]; ok {
			current += val
			found = true
		} else if scale, ok := scalesMap[w]; ok {
			if current == 0 {
				current = 1
			}
			if scale == 100 {
				current *= 100
			} else {
				total += current * scale
				current = 0
			}
			found = true
		}
	}
	total += current
	if found {
		return total
	}
	return 0
}

func extractSpokenAmount(cleaned, lower string) (int64, string, bool) {
	// 1. Prefix "k" with words or digits (e.g. "k-fifty", "k fifty", "k-20", "k-50", "k-hundred")
	if m := prefixKRegex.FindStringSubmatchIndex(lower); len(m) >= 4 {
		fullMatch := lower[m[0]:m[1]]
		token := lower[m[2]:m[3]]
		if val, ok := prefixKWordMap[token]; ok {
			return val * 100, fullMatch, true
		}
		if num, err := strconv.ParseFloat(token, 64); err == nil && num > 0 {
			return int64(num * 1000 * 100), fullMatch, true
		}
	}

	// 2. Suffix "k" on digits (e.g. "2k", "1.5k", "50k", "100k")
	if m := suffixKRegex.FindStringSubmatchIndex(lower); len(m) >= 4 {
		fullMatch := lower[m[0]:m[1]]
		numStr := lower[m[2]:m[3]]
		if num, err := strconv.ParseFloat(numStr, 64); err == nil && num > 0 {
			return int64(num * 1000 * 100), fullMatch, true
		}
	}

	// 3. Word(s) with "k" (e.g. "two k", "one point five k", "two and half k", "fifty k")
	if m := wordKRegex.FindStringSubmatchIndex(lower); len(m) >= 4 {
		fullMatch := lower[m[0]:m[1]]
		wordPart := strings.TrimSpace(lower[m[2]:m[3]])
		if strings.Contains(wordPart, "point five") {
			baseWord := strings.Fields(wordPart)[0]
			if baseVal, ok := unitsMap[baseWord]; ok {
				return int64((float64(baseVal) + 0.5) * 1000 * 100), fullMatch, true
			}
		} else if strings.Contains(wordPart, "and half") || strings.Contains(wordPart, "and a half") {
			baseWord := strings.Fields(wordPart)[0]
			if baseVal, ok := unitsMap[baseWord]; ok {
				return int64((float64(baseVal) + 0.5) * 1000 * 100), fullMatch, true
			}
		} else {
			val := parseNumberWords(wordPart)
			if val > 0 {
				return val * 1000 * 100, fullMatch, true
			}
		}
	}

	// 4. Shorthand <base>-five (e.g. "two-five", "one-five", "ten-five")
	if m := shorthandFiveRegex.FindStringSubmatchIndex(lower); len(m) >= 4 {
		fullMatch := lower[m[0]:m[1]]
		baseWord := lower[m[2]:m[3]]
		if baseVal, ok := unitsMap[baseWord]; ok && baseVal > 0 {
			return (baseVal*1000 + 500) * 100, fullMatch, true
		}
	}

	// 5. Shorthand <base>-fifty (e.g. "two fifty", "two-fifty", "seven fifty")
	if m := shorthandFiftyRegex.FindStringSubmatchIndex(lower); len(m) >= 4 {
		fullMatch := lower[m[0]:m[1]]
		baseWord := lower[m[2]:m[3]]
		if baseVal, ok := unitsMap[baseWord]; ok && baseVal > 0 {
			return (baseVal*100 + 50) * 100, fullMatch, true
		}
	}

	// 6. Standard amountRegex (numeric figures with currency markers or commas)
	matches := amountRegex.FindAllStringSubmatchIndex(cleaned, -1)
	for _, m := range matches {
		fullMatch := cleaned[m[0]:m[1]]
		numStr := ""
		for idx := 2; idx < len(m); idx += 2 {
			if m[idx] >= 0 && m[idx+1] >= 0 {
				numStr = cleaned[m[idx]:m[idx+1]]
				break
			}
		}
		if numStr != "" {
			rawNum := strings.ReplaceAll(numStr, ",", "")
			if val, err := strconv.ParseFloat(rawNum, 64); err == nil && val > 0 {
				return int64(val * 100), fullMatch, true
			}
		}
	}

	// 7. Spoken money words with naira/kobo or scale words
	if amt, phrase, ok := extractSpokenMoneyWithPhrase(lower); ok {
		return amt, phrase, true
	}

	return 0, "", false
}

// resolveSpokenQuantity returns the quantity, whether one was found, and whether
// it was inferred (defaulted to 1 because an amount was named but no quantity).
func resolveSpokenQuantity(cleaned, lower string, res ParsedAnchors) (int, bool, bool) {
	cleanLower := lower
	cleanCleaned := cleaned
	if res.AmountPhrase != "" {
		re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(res.AmountPhrase) + `\b`)
		cleanLower = re.ReplaceAllString(cleanLower, " ")
		cleanCleaned = re.ReplaceAllString(cleanCleaned, " ")
	}

	// If self-correction phrases exist ("wait no", "sorry", "i mean", etc.), prefer the corrected value after the marker
	markers := []string{"wait no", "no wait", "sorry", "i mean", "make that", "actually", "correction"}
	var lastMarkerIdx = -1
	var lastMarkerLen = 0
	for _, m := range markers {
		idx := strings.LastIndex(cleanLower, m)
		if idx > lastMarkerIdx {
			lastMarkerIdx = idx
			lastMarkerLen = len(m)
		}
	}

	if lastMarkerIdx >= 0 {
		afterClean := cleanCleaned[lastMarkerIdx+lastMarkerLen:]
		afterLower := strings.ToLower(afterClean)

		// Check digits after marker
		qtyPattern := regexp.MustCompile(`\b([0-9]+)\b`)
		for _, qm := range qtyPattern.FindAllStringSubmatch(afterClean, -1) {
			if val, err := strconv.Atoi(qm[1]); err == nil && val > 0 {
				if res.AmountFound && int64(val*100) == res.AmountMinor {
					continue
				}
				return val, true, false
			}
		}

		// Check words after marker
		if qty, ok := extractSpokenQuantity(afterLower); ok {
			return qty, true, false
		}
	}

	// Standard check: digits first
	qtyPattern := regexp.MustCompile(`\b([0-9]+)\b`)
	qtyMatches := qtyPattern.FindAllStringSubmatch(cleanCleaned, -1)
	for _, qm := range qtyMatches {
		if val, err := strconv.Atoi(qm[1]); err == nil && val > 0 {
			if res.AmountFound && int64(val*100) == res.AmountMinor {
				continue
			}
			return val, true, false
		}
	}

	// Words check
	if qty, ok := extractSpokenQuantity(cleanLower); ok {
		return qty, true, false
	}

	// If no explicit quantity was extracted, but an amount was found (e.g. "Two k Peak milk thank you"):
	// default quantity to 1 for the mentioned item only if a product mention exists.
	if res.AmountFound {
		prod := cleanProductMention(cleaned, res)
		if strings.Trim(prod, "- \t\r\n") != "" {
			return 1, true, true
		}
	}

	return 0, false, false
}

func extractSpokenQuantity(text string) (int, bool) {
	lower := strings.ToLower(text)
	// Check multi-word colloquial quantity phrases
	if strings.Contains(lower, "half dozen") {
		return 6, true
	}
	if strings.Contains(lower, "two dozen") {
		return 24, true
	}
	if strings.Contains(lower, "three dozen") {
		return 36, true
	}
	if strings.Contains(lower, "four dozen") {
		return 48, true
	}
	if strings.Contains(lower, "five dozen") {
		return 60, true
	}
	if strings.Contains(lower, "one dozen") || strings.Contains(lower, "a dozen") || regexp.MustCompile(`\bdozen\b`).MatchString(lower) {
		return 12, true
	}
	if strings.Contains(lower, "two score") {
		return 40, true
	}
	if strings.Contains(lower, "one score") || strings.Contains(lower, "a score") || regexp.MustCompile(`\bscore\b`).MatchString(lower) {
		return 20, true
	}

	// e.g. "two milo", "three cartons", "five packs"
	words := strings.Fields(strings.ReplaceAll(strings.ReplaceAll(text, ",", " "), "-", " "))
	for i, w := range words {
		// Stop if we hit currency indicators
		if w == "naira" || w == "kobo" || w == "thousand" || w == "million" || w == "k" {
			continue
		}

		// Check indefinite article before known unit: e.g. "a pack", "a carton", "a painter", "a mudu", "a derica", "a bottle"
		if (w == "a" || w == "an") && i+1 < len(words) {
			wNext := words[i+1]
			if _, ok := knownUnits[wNext]; ok {
				return 1, true
			}
			if wNext == "loaf" || wNext == "loaves" || wNext == "can" || wNext == "cans" {
				return 1, true
			}
		}

		if val, ok := unitsMap[w]; ok && val > 0 {
			// Skip if this word precedes a monetary scale or currency word (e.g. "four thousand", "four naira", "two k")
			if i+1 < len(words) && (words[i+1] == "thousand" || words[i+1] == "million" || words[i+1] == "hundred" || words[i+1] == "naira" || words[i+1] == "kobo" || words[i+1] == "k") {
				continue
			}
			// Skip if preceded by "k" (e.g. "k fifty")
			if i > 0 && words[i-1] == "k" {
				continue
			}
			// Skip if this is part of shorthand hundreds (e.g. "two fifty")
			if w == "fifty" && i > 0 {
				prev := words[i-1]
				if prev == "one" || prev == "two" || prev == "three" || prev == "four" || prev == "five" || prev == "six" || prev == "seven" || prev == "eight" || prev == "nine" || prev == "ten" {
					continue
				}
			}
			// Skip if this is part of shorthand thousands (e.g. "two five")
			if w == "five" && i > 0 {
				prev := words[i-1]
				if prev == "one" || prev == "two" || prev == "three" || prev == "four" || prev == "five" || prev == "six" || prev == "seven" || prev == "eight" || prev == "nine" || prev == "ten" || prev == "twelve" || prev == "fifteen" {
					continue
				}
			}

			// Check compound e.g. "twenty five"
			if i+1 < len(words) {
				if nextVal, ok2 := unitsMap[words[i+1]]; ok2 && val >= 20 && nextVal < 10 {
					return int(val + nextVal), true
				}
			}
			return int(val), true
		}
	}
	return 0, false
}

func extractSpokenMoney(text string) (int64, bool) {
	amt, _, ok := extractSpokenMoneyWithPhrase(text)
	return amt, ok
}

func extractSpokenMoneyWithPhrase(text string) (int64, string, bool) {
	// Look for "... naira" or "... kobo"
	idx := strings.Index(text, "naira")
	hasCurrencyWord := true
	currencyWord := "naira"
	if idx < 0 {
		idx = strings.Index(text, "kobo")
		currencyWord = "kobo"
	}
	if idx < 0 {
		hasCurrencyWord = false
		// If neither naira nor kobo, check if text contains scale words like "thousand", "million"
		// or "hundred"
		for _, scale := range []string{"thousand", "million", "hundred"} {
			sIdx := strings.LastIndex(text, scale)
			if sIdx >= 0 {
				endIdx := sIdx + len(scale)
				afterScale := text[endIdx:]
				afterWords := strings.Fields(afterScale)
				for _, aw := range afterWords {
					if _, isU := unitsMap[aw]; isU {
						endIdx = strings.Index(text[endIdx:], aw) + endIdx + len(aw)
					} else {
						break
					}
				}
				idx = endIdx
				break
			}
		}
	}
	if idx < 0 {
		return 0, "", false
	}
	target := text[:idx]

	words := strings.Fields(strings.ReplaceAll(strings.ReplaceAll(target, ",", " "), "-", " "))
	// Trace backwards from the end of target words, collecting ONLY contiguous number tokens
	var numWords []string
	for i := len(words) - 1; i >= 0; i-- {
		w := words[i]
		_, isUnit := unitsMap[w]
		_, isScale := scalesMap[w]
		if isUnit || isScale {
			numWords = append([]string{w}, numWords...)
		} else {
			if len(numWords) > 0 {
				break
			}
		}
	}

	if len(numWords) == 0 {
		return 0, "", false
	}

	var current int64
	var total int64
	foundNumber := false

	for _, w := range numWords {
		if val, ok := unitsMap[w]; ok {
			current += val
			foundNumber = true
		} else if scale, ok := scalesMap[w]; ok {
			if current == 0 {
				current = 1
			}
			if scale == 100 {
				current *= 100
			} else {
				total += current * scale
				current = 0
			}
			foundNumber = true
		}
	}
	total += current

	if foundNumber && total > 0 {
		phrase := strings.Join(numWords, " ")
		if hasCurrencyWord {
			phrase = phrase + " " + currencyWord
		}
		// Convert whole currency to minor units (x100)
		return total * 100, phrase, true
	}
	return 0, "", false
}

func cleanProductMention(raw string, anchors ParsedAnchors) string {
	s := raw
	if anchors.AmountPhrase != "" {
		s = regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(anchors.AmountPhrase)+`\b`).ReplaceAllString(s, " ")
	}

	// Remove currency phrases
	s = regexp.MustCompile(`(?i)\b(?:ngn\s*)?[0-9,]+(?:\.[0-9]+)?\s*(?:naira|kobo|ngn)?\b`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`(?i)\b(?:thousand|million|hundred|naira|kobo|ngn)\b`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`(?i)\bk[- ]?(?:fifty|forty|thirty|twenty|ten|five|hundred|[0-9]+)\b`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`(?i)\b[0-9]+(?:\.[0-9]+)?\s*k\b`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`(?i)\b(?:one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen|twenty|thirty|forty|fifty|hundred)\s+k\b`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`(?i)\b(?:one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|sixteen|seventeen|eighteen|nineteen)[- ]five\b`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`(?i)\b(?:one|two|three|four|five|six|seven|eight|nine|ten)[- ]fifty\b`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`(?i)\b(?:one|two|three|four|five)\s+point\s+five(?:\s+k)?\b`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`(?i)\b(?:one|two|three|four|five)\s+and\s+(?:a\s+)?half(?:\s+k)?\b`).ReplaceAllString(s, " ")

	// Remove units
	for u := range knownUnits {
		s = regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(u)+`\b`).ReplaceAllString(s, " ")
	}

	// Remove quantity words and phrases at word boundaries
	s = regexp.MustCompile(`(?i)\b(?:half\s+dozen|a\s+dozen|one\s+dozen|dozen|two\s+dozen|three\s+dozen|four\s+dozen|five\s+dozen|two\s+score|one\s+score|a\s+score|score|pair|pairs)\b`).ReplaceAllString(s, " ")
	for w := range unitsMap {
		s = regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(w)+`\b`).ReplaceAllString(s, " ")
	}
	s = regexp.MustCompile(`\b[0-9]+\b`).ReplaceAllString(s, " ")

	// Remove conversational greetings, correction markers, fillers, supplier prefixes, customer/credit words, boundaries, articles, and noise words
	s = regexp.MustCompile(`(?i)\b(?:good\s+morning|good\s+afternoon|good\s+evening|how\s+are\s+you|how\s+far|hello|hi|welcome|sorry|wait\s+no|no\s+wait|wait|no|i\s+mean|make\s+that|actually|correction|uh|um|ah|er|eh|okay|alright|of|and|a|an|the|please|for|at|that\s+is\s+all|delivery\s+from|received\s+from|restock\s+from|from\s+supplier|supplier|delivery|restock|credit|tab|debt|account|customer|cust|thank\s+you|thanks|stock\s+received|restock\s+complete|purchase\s+received)\b`).ReplaceAllString(s, " ")

	var b strings.Builder
	for _, r := range s {
		if r == '-' || r == ',' || r == '.' || r == ';' || r == '"' || r == '\'' || r == '“' || r == '”' {
			b.WriteRune(' ')
		} else {
			b.WriteRune(r)
		}
	}

	return strings.TrimSpace(regexp.MustCompile(`\s+`).ReplaceAllString(b.String(), " "))
}

// DetectBoundary searches the end or body of the transcript for known closing phrases.
func DetectBoundary(transcript, salePhrase, restockPhrase string) (models.TransactionType, string, bool) {
	lower := strings.ToLower(transcript)
	if salePhrase == "" {
		salePhrase = "thank you"
	}
	if restockPhrase == "" {
		restockPhrase = "stock received"
	}

	lowerSale := strings.ToLower(salePhrase)
	lowerRestock := strings.ToLower(restockPhrase)

	// Check restock boundary first
	if strings.Contains(lower, lowerRestock) || strings.Contains(lower, "restock complete") || strings.Contains(lower, "purchase received") {
		return models.TypeRestock, restockPhrase, true
	}

	// Check sale boundary
	if strings.Contains(lower, lowerSale) {
		return models.TypeSale, salePhrase, true
	}

	return "", "", false
}

var connectiveRegex = regexp.MustCompile(`(?i)(?:,\s*|\s+)(?:and\s+also|and\s+then|also|plus|and|with|then)\s+|(?:,\s*)`)

func startsWithQuantity(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}
	// 1. Starts with digit: e.g. "2 milo", "30 cartons"
	if trimmed[0] >= '0' && trimmed[0] <= '9' {
		return true
	}
	// 2. Starts with a known quantity word
	words := strings.Fields(strings.ReplaceAll(strings.ReplaceAll(trimmed, ",", " "), "-", " "))
	if len(words) == 0 {
		return false
	}
	w0 := strings.ToLower(words[0])
	if val, ok := unitsMap[w0]; ok && val > 0 {
		return true
	}
	if w0 == "dozen" || w0 == "half" || w0 == "pair" || w0 == "score" {
		return true
	}
	// 3. Starts with indefinite article + packaging unit: e.g. "a pack", "a carton", "a bottle", "a tin", "a crate", "a piece"
	if (w0 == "a" || w0 == "an") && len(words) > 1 {
		w1 := strings.ToLower(words[1])
		if _, ok := knownUnits[w1]; ok {
			return true
		}
		if w1 == "loaf" || w1 == "loaves" || w1 == "bag" || w1 == "bags" || w1 == "sachet" || w1 == "sachets" || w1 == "can" || w1 == "cans" {
			return true
		}
	}
	return false
}

func containsSelfCorrection(s string) bool {
	lower := strings.ToLower(s)
	for _, m := range []string{"wait no", "no wait", "sorry", "i mean", "make that", "actually", "correction"} {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func isMoneyAmount(s string) bool {
	anchors := ParseSpokenSegment(s, "")
	cleanProd := strings.Trim(anchors.ProductMention, "- \t\r\n")
	if anchors.AmountFound && cleanProd == "" {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(s))
	if strings.HasSuffix(lower, "ngn") || strings.HasSuffix(lower, "naira") || strings.HasSuffix(lower, "kobo") || strings.HasSuffix(lower, "k") {
		cleaned := cleanProductMention(s, anchors)
		if strings.Trim(cleaned, "- \t\r\n") == "" {
			return true
		}
	}
	return false
}

// SplitCompoundUtterance splits continuous compound phrases (e.g. "2 Milo and 3 Indomie", "5 milk, 2 sugar, plus 1 bread")
// into individual line item clauses while preserving compound product names like "Bread and Butter"
// and self-corrections like "Two Milo, wait no, three Milo".
func SplitCompoundUtterance(text string) []string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil
	}

	if containsSelfCorrection(trimmed) {
		return []string{trimmed}
	}

	locs := connectiveRegex.FindAllStringIndex(trimmed, -1)
	if len(locs) == 0 {
		return []string{trimmed}
	}

	var boundaries []int // pairs of [startMatch, endMatch]
	for _, loc := range locs {
		afterIdx := loc[1]
		if afterIdx >= len(trimmed) {
			continue
		}
		afterText := strings.TrimSpace(trimmed[afterIdx:])
		if startsWithQuantity(afterText) && !isMoneyAmount(afterText) {
			boundaries = append(boundaries, loc[0], loc[1])
		}
	}

	if len(boundaries) == 0 {
		return []string{trimmed}
	}

	var segments []string
	prevEnd := 0
	for i := 0; i < len(boundaries); i += 2 {
		startMatch := boundaries[i]
		endMatch := boundaries[i+1]
		seg := strings.TrimSpace(trimmed[prevEnd:startMatch])
		if seg != "" {
			segments = append(segments, seg)
		}
		prevEnd = endMatch
	}
	tail := strings.TrimSpace(trimmed[prevEnd:])
	if tail != "" {
		segments = append(segments, tail)
	}

	return segments
}

// SplitTranscriptLines splits a multi-line spoken receipt into line segments.
func SplitTranscriptLines(transcript, closingPhrase string) []string {
	text := transcript
	if closingPhrase != "" {
		// Strip the closing phrase
		idx := strings.LastIndex(strings.ToLower(text), strings.ToLower(closingPhrase))
		if idx >= 0 {
			text = text[:idx]
		}
	}

	// Split by periods, semicolons, or newlines
	rawSegments := regexp.MustCompile(`[.;\n]+`).Split(text, -1)
	var lines []string
	for _, seg := range rawSegments {
		trimmed := strings.TrimSpace(seg)
		if trimmed != "" {
			compoundItems := SplitCompoundUtterance(trimmed)
			lines = append(lines, compoundItems...)
		}
	}
	if len(lines) == 0 && strings.TrimSpace(text) != "" {
		compoundItems := SplitCompoundUtterance(strings.TrimSpace(text))
		lines = append(lines, compoundItems...)
	}

	// Merge orphaned amount segments into preceding product line
	var merged []string
	for _, l := range lines {
		if len(merged) > 0 {
			prevAnchors := ParseSpokenSegment(merged[len(merged)-1], "")
			curAnchors := ParseSpokenSegment(l, "")
			if curAnchors.AmountFound && curAnchors.ProductMention == "" && !prevAnchors.AmountFound {
				merged[len(merged)-1] = merged[len(merged)-1] + ", " + l
				continue
			}
		}
		merged = append(merged, l)
	}

	return merged
}

// cancellationPhrases cancel wherever they appear as whole words: each is
// unambiguous on its own.
var cancellationPhrases = []string{
	"cancel sale",
	"cancel the sale",
	"cancel transaction",
	"cancel this transaction",
	"customer cancelled",
	"customer canceled",
	"cancel that",
	"cancel this",
	"never mind",
	"don't record",
	"do not record",
	"cancel restock",
	"scratch that",
	"stop recording",
}

// trailingCancellationPhrases are ordinary verbs with an implicit object
// ("drop that"). They only cancel when they are the whole utterance or its
// trailing clause, so "drop that bag there" stays a shop instruction instead of
// silently voiding a sale (README.md#history, historical audit finding 5 / P5.3).
var trailingCancellationPhrases = []string{
	"drop that",
}

var cancellationPhraseRegexps = func() []*regexp.Regexp {
	regexps := make([]*regexp.Regexp, 0, len(cancellationPhrases))
	for _, phrase := range cancellationPhrases {
		regexps = append(regexps, regexp.MustCompile(`\b`+regexp.QuoteMeta(phrase)+`\b`))
	}
	return regexps
}()

// DetectCancellation checks if the spoken transcript contains an in-flight
// cancellation phrase (blueprint §18). Matching is word-bounded so a phrase
// buried inside a longer word cannot cancel a sale.
func DetectCancellation(transcript string) (string, bool) {
	lower := strings.ToLower(transcript)
	for i, re := range cancellationPhraseRegexps {
		if re.MatchString(lower) {
			return cancellationPhrases[i], true
		}
	}

	normalized := strings.Trim(lower, " \t\r\n.,!?;:")
	for _, phrase := range trailingCancellationPhrases {
		if normalized == phrase || strings.HasSuffix(normalized, " "+phrase) {
			return phrase, true
		}
	}
	return "", false
}

var supplierPrefixRegex = regexp.MustCompile(`(?i)^(?:delivery\s+from|received\s+from|restock\s+from|from\s+supplier|supplier|from)\s+([A-Za-z0-9&'.\s-]+?)(?::|\s*[-–—]\s*|\s*,\s*|\s+(?:we\s+got|got|received|brought|delivered|items?|products?|[0-9]+|\b(?:one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|twenty|thirty|forty|fifty|hundred)\b))`)

// ExtractSupplier parses spoken supplier attribution from restock utterances (blueprint §18).
// Returns the clean supplier name and the transcript with the supplier clause stripped.
func ExtractSupplier(text string) (string, string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", text
	}

	match := supplierPrefixRegex.FindStringSubmatchIndex(trimmed)
	if len(match) >= 4 {
		nameStart, nameEnd := match[2], match[3]
		candidate := strings.TrimSpace(trimmed[nameStart:nameEnd])
		lower := strings.ToLower(candidate)
		// Discard noise matches like "today", "yesterday", "the store"
		if candidate != "" && lower != "today" && lower != "yesterday" && lower != "the" && lower != "warehouse" && lower != "store" {
			prefixSlice := trimmed[match[0]:match[1]]
			sepIdx := strings.IndexAny(prefixSlice, ":-–—,")
			var remaining string
			if sepIdx >= 0 {
				remaining = strings.TrimSpace(trimmed[match[0]+sepIdx+1:])
			} else {
				remaining = strings.TrimSpace(trimmed[nameEnd:])
			}
			cleanName := strings.Trim(candidate, ":-–—, \t\r\n")
			return cleanName, remaining
		}
	}

	return "", text
}

var (
	creditSuffixRegex1       = regexp.MustCompile(`(?i)(?:,\s*|\s+)(?:for|to)\s+([A-Za-z0-9&'.\s-]+?)\s+on\s+(?:credit|tab|debt|account)(?:[.,;:\s]+(.*)|$)`)
	creditSuffixRegex2       = regexp.MustCompile(`(?i)(?:,\s*|\s+)on\s+(?:credit|tab|debt|account)\s+(?:for|to)\s+([A-Za-z0-9&'.\s-]+?)(?:[.,;:\s]+(.*)|$)`)
	putOnTabPrefixRegex      = regexp.MustCompile(`(?i)^(?:put\s+(?:it\s+)?on|charge\s+(?:to\s+)?|add\s+to)\s+([A-Za-z0-9&'.\s-]+?)(?:'s)?\s+(?:tab|debt|credit|account)\s*(?::|\s*[-–—]\s*|\s*,\s*|\s+(?:we\s+got|[0-9]+|\b(?:one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|twenty|thirty|forty|fifty|hundred|a|an)\b))`)
	creditSalePrefixRegex    = regexp.MustCompile(`(?i)^(?:credit\s+sale\s+(?:for|to\s+|)|on\s+credit\s+(?:for|to\s+|)|credit\s+(?:for|to))\s+([A-Za-z0-9&'.\s-]+?)(?::|\s*[-–—]\s*|\s*,\s*|\s+(?:[0-9]+|\b(?:one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|thirteen|fourteen|fifteen|twenty|thirty|forty|fifty|hundred|a|an)\b))`)
	customerPrefixRegex      = regexp.MustCompile(`(?i)^(?:customer|cust|for\s+customer)\s*(?::|\s*[-–—]\s*|\s*,\s*|\s+)\s*([A-Za-z0-9&'.\s-]+?)(?::|\s*[-–—]\s*|\s*,\s*)`)
	genericCreditPhraseRegex = regexp.MustCompile(`(?i)(?:,\s*|\s+)\b(?:on\s+credit|on\s+tab|on\s+debt|on\s+account)\b(?:[.,;:\s]+(.*)|$)`)
)

// ExtractCustomer parses spoken customer credit attribution from counter utterances (blueprint §18).
// Returns customer name, whether credit/tab was specified, and the remaining utterance.
func ExtractCustomer(text string) (string, bool, string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "", false, text
	}

	// 1. Check Put on Tab prefix ("Put on Emeka's tab: 2 Milo", "Charge to Mama Ngozi's account: 3 bread")
	if match := putOnTabPrefixRegex.FindStringSubmatchIndex(trimmed); len(match) >= 4 {
		nameStart, nameEnd := match[2], match[3]
		candidate := cleanSpokenCustomerName(trimmed[nameStart:nameEnd])
		if isValidCustomerCandidate(candidate) {
			afterName := trimmed[nameEnd:]
			afterLower := strings.ToLower(afterName)
			idx := -1
			keywordLen := 0
			for _, kw := range []string{"tab", "credit", "debt", "account"} {
				kIdx := strings.Index(afterLower, kw)
				if kIdx >= 0 && (idx < 0 || kIdx < idx) {
					idx = kIdx
					keywordLen = len(kw)
				}
			}
			var remaining string
			if idx >= 0 {
				remaining = strings.TrimLeft(afterName[idx+keywordLen:], ":-–—, \t\r\n")
			} else {
				remaining = strings.TrimLeft(afterName, ":-–—, \t\r\n")
			}
			return candidate, true, strings.TrimSpace(remaining)
		}
	}

	// 2. Check Credit Sale prefix ("Credit sale for Emeka: 2 Milo", "On credit to Mama Ngozi: 5 Peak milk")
	if match := creditSalePrefixRegex.FindStringSubmatchIndex(trimmed); len(match) >= 4 {
		nameStart, nameEnd := match[2], match[3]
		candidate := cleanSpokenCustomerName(trimmed[nameStart:nameEnd])
		if isValidCustomerCandidate(candidate) {
			remaining := strings.TrimLeft(trimmed[nameEnd:], ":-–—, \t\r\n")
			return candidate, true, strings.TrimSpace(remaining)
		}
	}

	// 3. Check Suffix 1 ("2 Milo for Emeka on credit, thank you")
	if match := creditSuffixRegex1.FindStringSubmatchIndex(trimmed); len(match) >= 4 {
		nameStart, nameEnd := match[2], match[3]
		candidate := cleanSpokenCustomerName(trimmed[nameStart:nameEnd])
		if isValidCustomerCandidate(candidate) {
			pre := strings.TrimSpace(trimmed[:match[0]])
			trailing := ""
			if len(match) >= 6 && match[4] >= 0 && match[5] >= 0 {
				trailing = strings.TrimSpace(trimmed[match[4]:match[5]])
			}
			remaining := pre
			if trailing != "" {
				remaining = pre + ", " + trailing
			}
			return candidate, true, strings.TrimSpace(remaining)
		}
	}

	// 4. Check Suffix 2 ("2 Milo on credit for Emeka, thank you")
	if match := creditSuffixRegex2.FindStringSubmatchIndex(trimmed); len(match) >= 4 {
		nameStart, nameEnd := match[2], match[3]
		candidate := cleanSpokenCustomerName(trimmed[nameStart:nameEnd])
		if isValidCustomerCandidate(candidate) {
			pre := strings.TrimSpace(trimmed[:match[0]])
			trailing := ""
			if len(match) >= 6 && match[4] >= 0 && match[5] >= 0 {
				trailing = strings.TrimSpace(trimmed[match[4]:match[5]])
			}
			remaining := pre
			if trailing != "" {
				remaining = pre + ", " + trailing
			}
			return candidate, true, strings.TrimSpace(remaining)
		}
	}

	// 5. Check Customer prefix ("Customer Emeka: 2 Milo", "Customer Emeka on credit: 2 Milo")
	if match := customerPrefixRegex.FindStringSubmatchIndex(trimmed); len(match) >= 4 {
		nameStart, nameEnd := match[2], match[3]
		rawCand := trimmed[nameStart:nameEnd]
		isCredit := false
		lowerCand := strings.ToLower(rawCand)
		if strings.Contains(lowerCand, "on credit") || strings.Contains(lowerCand, "on tab") || strings.Contains(lowerCand, "on debt") {
			isCredit = true
			rawCand = regexp.MustCompile(`(?i)\s+on\s+(?:credit|tab|debt|account)`).ReplaceAllString(rawCand, "")
		}
		candidate := cleanSpokenCustomerName(rawCand)
		if isValidCustomerCandidate(candidate) {
			remaining := strings.TrimSpace(trimmed[match[1]:])
			return candidate, isCredit, remaining
		}
	}

	// 6. Generic anonymous credit phrase ("2 Milo on credit, thank you")
	if match := genericCreditPhraseRegex.FindStringSubmatchIndex(trimmed); len(match) >= 2 {
		pre := strings.TrimSpace(trimmed[:match[0]])
		trailing := ""
		if len(match) >= 4 && match[2] >= 0 && match[3] >= 0 {
			trailing = strings.TrimSpace(trimmed[match[2]:match[3]])
		}
		remaining := pre
		if trailing != "" {
			remaining = pre + ", " + trailing
		}
		return "", true, strings.TrimSpace(remaining)
	}

	return "", false, text
}

func cleanSpokenCustomerName(raw string) string {
	s := strings.Trim(raw, ":-–—, '\"\t\r\n")
	s = strings.TrimSuffix(s, "'s")
	s = strings.TrimSuffix(s, "’s")
	s = strings.TrimSuffix(s, "'S")
	return strings.TrimSpace(s)
}

func isValidCustomerCandidate(name string) bool {
	if name == "" {
		return false
	}
	lower := strings.ToLower(name)
	switch lower {
	case "today", "yesterday", "cash", "store", "warehouse", "shop", "sale", "restock", "the", "a", "an":
		return false
	default:
		return true
	}
}

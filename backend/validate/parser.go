package validate

import (
	"regexp"
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
		"carton":  models.Unit("carton"),
		"cartons": models.Unit("carton"),
		"pack":    models.Unit("pack"),
		"packs":   models.Unit("pack"),
		"bottle":  models.Unit("bottle"),
		"bottles": models.Unit("bottle"),
		"piece":   models.Unit("piece"),
		"pieces":  models.Unit("piece"),
		"tin":     models.Unit("tin"),
		"tins":    models.Unit("tin"),
		"crate":   models.Unit("crate"),
		"crates":  models.Unit("crate"),
	}

	amountRegex = regexp.MustCompile(`(?i)\b(?:ngn\s*([0-9]+(?:,[0-9]{3})*(?:\.[0-9]+)?)|([0-9]+(?:,[0-9]{3})*(?:\.[0-9]+)?)\s*(?:naira|kobo|ngn)|([0-9]{1,3}(?:,[0-9]{3})+(?:\.[0-9]+)?))\b`)
)

// ParsedAnchors contains raw tokens extracted from a spoken segment.
type ParsedAnchors struct {
	ProductMention string
	Quantity       int
	QuantityFound  bool
	Unit           models.Unit
	UnitFound      bool
	AmountMinor    int64
	AmountFound    bool
	Currency       string
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

	// 1. Extract Unit
	lower := strings.ToLower(cleaned)
	for word, unit := range knownUnits {
		// match standalone word boundary
		pattern := `\b` + regexp.QuoteMeta(word) + `\b`
		if matched, _ := regexp.MatchString(pattern, lower); matched {
			res.Unit = unit
			res.UnitFound = true
			break
		}
	}

	// 2. Extract Amount
	// Check for numeric amount with or without currency indicator
	matches := amountRegex.FindAllStringSubmatch(cleaned, -1)
	for _, m := range matches {
		numStr := ""
		for idx := 1; idx < len(m); idx++ {
			if m[idx] != "" {
				numStr = m[idx]
				break
			}
		}
		if numStr != "" {
			rawNum := strings.ReplaceAll(numStr, ",", "")
			if val, err := strconv.ParseFloat(rawNum, 64); err == nil && val > 0 {
				// Amount in kobo (x100)
				res.AmountMinor = int64(val * 100)
				res.AmountFound = true
				break
			}
		}
	}

	// If no numeric amount was matched, check spoken words: e.g. "four thousand naira", "forty-five thousand naira"
	if !res.AmountFound {
		if amt, ok := extractSpokenMoney(lower); ok {
			res.AmountMinor = amt
			res.AmountFound = true
		}
	}

	// 3. Extract Quantity with self-correction handling
	if qty, ok := resolveSpokenQuantity(cleaned, lower, res); ok {
		res.Quantity = qty
		res.QuantityFound = true
	}

	// 4. Extract Product Mention
	// Strip extracted numbers, units, amount phrases, fillers, and punctuation
	productText := cleanProductMention(cleaned, res)
	res.ProductMention = productText

	return res
}

func resolveSpokenQuantity(cleaned, lower string, res ParsedAnchors) (int, bool) {
	// If self-correction phrases exist ("wait no", "sorry", "i mean", etc.), prefer the corrected value after the marker
	markers := []string{"wait no", "no wait", "sorry", "i mean", "make that", "actually", "correction"}
	var lastMarkerIdx = -1
	var lastMarkerLen = 0
	for _, m := range markers {
		idx := strings.LastIndex(lower, m)
		if idx > lastMarkerIdx {
			lastMarkerIdx = idx
			lastMarkerLen = len(m)
		}
	}

	if lastMarkerIdx >= 0 {
		afterClean := cleaned[lastMarkerIdx+lastMarkerLen:]
		afterLower := strings.ToLower(afterClean)

		// Check digits after marker
		qtyPattern := regexp.MustCompile(`\b([0-9]+)\b`)
		for _, qm := range qtyPattern.FindAllStringSubmatch(afterClean, -1) {
			if val, err := strconv.Atoi(qm[1]); err == nil && val > 0 {
				if res.AmountFound && int64(val*100) == res.AmountMinor {
					continue
				}
				return val, true
			}
		}

		// Check words after marker
		if qty, ok := extractSpokenQuantity(afterLower); ok {
			return qty, true
		}
	}

	// Standard check: digits first
	qtyPattern := regexp.MustCompile(`\b([0-9]+)\b`)
	qtyMatches := qtyPattern.FindAllStringSubmatch(cleaned, -1)
	for _, qm := range qtyMatches {
		if val, err := strconv.Atoi(qm[1]); err == nil && val > 0 {
			if res.AmountFound && int64(val*100) == res.AmountMinor {
				continue
			}
			return val, true
		}
	}

	// Fallback to words
	return extractSpokenQuantity(lower)
}

func extractSpokenQuantity(text string) (int, bool) {
	// e.g. "two milo", "three cartons", "five packs"
	words := strings.Fields(strings.ReplaceAll(strings.ReplaceAll(text, ",", " "), "-", " "))
	for i, w := range words {
		// Stop if we hit currency indicators
		if w == "naira" || w == "kobo" || w == "thousand" || w == "million" {
			continue
		}
		if val, ok := unitsMap[w]; ok && val > 0 {
			// Skip if this word precedes a monetary scale or currency word (e.g. "four thousand", "four naira")
			if i+1 < len(words) && (words[i+1] == "thousand" || words[i+1] == "million" || words[i+1] == "hundred" || words[i+1] == "naira" || words[i+1] == "kobo") {
				continue
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
	// Look for "... naira" or "... kobo"
	idx := strings.Index(text, "naira")
	if idx < 0 {
		idx = strings.Index(text, "kobo")
	}
	if idx < 0 {
		return 0, false
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
		return 0, false
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
		// Convert whole currency to minor units (x100)
		return total * 100, true
	}
	return 0, false
}

func cleanProductMention(raw string, anchors ParsedAnchors) string {
	s := raw
	// Remove currency phrases
	s = regexp.MustCompile(`(?i)\b(?:ngn\s*)?[0-9,]+(?:\.[0-9]+)?\s*(?:naira|kobo|ngn)?\b`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`(?i)\b(?:thousand|million|hundred|naira|kobo|ngn)\b`).ReplaceAllString(s, " ")

	// Remove units
	for u := range knownUnits {
		s = regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(u)+`\b`).ReplaceAllString(s, " ")
	}

	// Remove quantity words at word boundaries
	for w := range unitsMap {
		s = regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(w)+`\b`).ReplaceAllString(s, " ")
	}
	s = regexp.MustCompile(`\b[0-9]+\b`).ReplaceAllString(s, " ")

	// Remove conversational greetings, correction markers, fillers, supplier prefixes, customer/credit words, and noise words
	s = regexp.MustCompile(`(?i)\b(?:good\s+morning|good\s+afternoon|good\s+evening|how\s+are\s+you|how\s+far|hello|hi|welcome|sorry|wait\s+no|no\s+wait|wait|no|i\s+mean|make\s+that|actually|correction|uh|um|ah|er|eh|okay|alright|of|and|please|for|at|that\s+is\s+all|delivery\s+from|received\s+from|restock\s+from|from\s+supplier|supplier|delivery|restock|credit|tab|debt|account|customer|cust)\b`).ReplaceAllString(s, " ")

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
	if w0 == "dozen" || w0 == "half" || w0 == "pair" {
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
	if strings.HasSuffix(lower, "ngn") || strings.HasSuffix(lower, "naira") || strings.HasSuffix(lower, "kobo") {
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
			if curAnchors.AmountFound && curAnchors.ProductMention == "" && curAnchors.Quantity == 0 && !prevAnchors.AmountFound {
				merged[len(merged)-1] = merged[len(merged)-1] + ", " + l
				continue
			}
		}
		merged = append(merged, l)
	}

	return merged
}

var cancellationPhrases = []string{
	"cancel sale",
	"cancel the sale",
	"cancel transaction",
	"cancel this transaction",
	"customer cancelled",
	"customer canceled",
	"cancel that",
	"cancel this",
	"drop that",
	"never mind",
	"don't record",
	"do not record",
	"cancel restock",
	"scratch that",
	"stop recording",
}

// DetectCancellation checks if the spoken transcript contains an in-flight cancellation phrase (blueprint §18).
func DetectCancellation(transcript string) (string, bool) {
	lower := strings.ToLower(transcript)
	for _, phrase := range cancellationPhrases {
		if strings.Contains(lower, phrase) {
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

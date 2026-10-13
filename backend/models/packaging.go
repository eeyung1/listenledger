package models

import (
	"errors"
	"math"
)

var ErrPackaging = errors.New("invalid or unsupported packaging conversion")

// ValidatePackaging permits one unambiguous directed conversion per unit, with no cycles.
func ValidatePackaging(base Unit, rules []PackagingRelation) error {
	if !base.Valid() {
		return ErrPackaging
	}
	next := map[Unit]PackagingRelation{}
	for _, r := range rules {
		if !r.From.Valid() || !r.To.Valid() || r.From == r.To || r.Factor <= 0 || r.From == base {
			return ErrPackaging
		}
		if _, ok := next[r.From]; ok {
			return ErrPackaging
		}
		next[r.From] = r
	}
	for from := range next {
		seen := map[Unit]bool{}
		for from != base {
			if seen[from] {
				return ErrPackaging
			}
			seen[from] = true
			r, ok := next[from]
			if !ok {
				return ErrPackaging
			}
			from = r.To
		}
	}
	return nil
}
func BaseQuantity(p Product, quantity int, unit Unit) (int, error) {
	if quantity <= 0 || !unit.Valid() {
		return 0, ErrPackaging
	}
	if err := ValidatePackaging(p.BaseUnit, p.Packaging); err != nil {
		return 0, err
	}
	next := map[Unit]PackagingRelation{}
	for _, r := range p.Packaging {
		next[r.From] = r
	}
	for unit != p.BaseUnit {
		r, ok := next[unit]
		if !ok || quantity > math.MaxInt/r.Factor {
			return 0, ErrPackaging
		}
		quantity *= r.Factor
		unit = r.To
	}
	return quantity, nil
}

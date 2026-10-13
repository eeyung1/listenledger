package models

import (
	"math"
	"testing"
)

func TestPackagingConversion(t *testing.T) {
	p := Product{BaseUnit: UnitBottle, Packaging: []PackagingRelation{{UnitCarton, UnitPack, 12}, {UnitPack, UnitBottle, 6}}}
	if n, e := BaseQuantity(p, 3, UnitCarton); e != nil || n != 216 {
		t.Fatal(n, e)
	}
	if _, e := BaseQuantity(p, math.MaxInt, UnitCarton); e == nil {
		t.Fatal("overflow accepted")
	}
	for _, rules := range [][]PackagingRelation{{{UnitCarton, UnitPack, 12}, {UnitPack, UnitCarton, 2}}, {{UnitCarton, UnitPack, 12}, {UnitCarton, UnitBottle, 6}}, {{UnitCarton, UnitPack, 0}}, {{UnitPack, UnitCrate, 2}}} {
		if ValidatePackaging(UnitBottle, rules) == nil {
			t.Fatal("invalid rules accepted", rules)
		}
	}
}

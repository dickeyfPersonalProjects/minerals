package domain

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestNewID_ReturnsUUIDv7(t *testing.T) {
	id := NewID()
	if id == uuid.Nil {
		t.Fatal("NewID returned the nil UUID")
	}
	if got := id.Version(); got != 7 {
		t.Fatalf("NewID returned UUID v%d, want v7", got)
	}
}

func TestNewID_ProducesDistinctValues(t *testing.T) {
	const n = 32
	seen := make(map[uuid.UUID]struct{}, n)
	for i := 0; i < n; i++ {
		id := NewID()
		if _, dup := seen[id]; dup {
			t.Fatalf("NewID returned a duplicate at iter %d: %s", i, id)
		}
		seen[id] = struct{}{}
	}
}

func ptr[T any](v T) *T { return &v }

func TestMineralData_Validate(t *testing.T) {
	// Empty struct (all pointers nil) is valid — type_data fields
	// are sparse/optional in v1.
	if err := (MineralData{}).Validate(); err != nil {
		t.Errorf("empty MineralData.Validate(): %v", err)
	}
	good := MineralData{MohsHardness: ptr(7.5), Color: ptr("blue")}
	if err := good.Validate(); err != nil {
		t.Errorf("good MineralData.Validate(): %v", err)
	}

	bad := MineralData{MohsHardness: ptr(11.5)}
	err := bad.Validate()
	if !errors.Is(err, ErrSpecimenTypeDataInvalid) {
		t.Errorf("MohsHardness=11.5: got %v, want ErrSpecimenTypeDataInvalid", err)
	}
	negative := MineralData{MohsHardness: ptr(-1.0)}
	if err := negative.Validate(); !errors.Is(err, ErrSpecimenTypeDataInvalid) {
		t.Errorf("MohsHardness=-1: got %v, want ErrSpecimenTypeDataInvalid", err)
	}
}

func TestRockData_Validate(t *testing.T) {
	if err := (RockData{}).Validate(); err != nil {
		t.Errorf("empty RockData.Validate(): %v", err)
	}
	for _, ok := range []string{"igneous", "sedimentary", "metamorphic"} {
		if err := (RockData{RockType: ptr(ok)}).Validate(); err != nil {
			t.Errorf("RockData{%q}.Validate(): %v", ok, err)
		}
	}
	err := (RockData{RockType: ptr("plutonic")}).Validate()
	if !errors.Is(err, ErrSpecimenTypeDataInvalid) {
		t.Errorf("RockType=plutonic: got %v", err)
	}
}

func TestFossilData_Validate(t *testing.T) {
	// Every FossilData field is free-form in v1; Validate accepts
	// any combination, including a fully populated struct.
	if err := (FossilData{}).Validate(); err != nil {
		t.Errorf("empty FossilData.Validate(): %v", err)
	}
	full := FossilData{
		Taxon:            ptr("Tyrannosaurus rex"),
		TaxonomicGroup:   ptr("Dinosauria"),
		GeologicPeriod:   ptr("Cretaceous"),
		Formation:        ptr("Hell Creek Formation"),
		Locality:         ptr("Montana, USA"),
		PreservationType: ptr("Permineralized"),
		Completeness:     ptr("Partial"),
		Prepared:         ptr(true),
		PrepNotes:        ptr("Air-abrasion only"),
	}
	if err := full.Validate(); err != nil {
		t.Errorf("full FossilData.Validate(): %v", err)
	}
}

func TestMeteoriteData_Validate(t *testing.T) {
	if err := (MeteoriteData{}).Validate(); err != nil {
		t.Errorf("empty MeteoriteData.Validate(): %v", err)
	}
	if err := (MeteoriteData{FallOrFind: ptr("fall")}).Validate(); err != nil {
		t.Errorf("FallOrFind=fall: %v", err)
	}
	if err := (MeteoriteData{FallOrFind: ptr("find")}).Validate(); err != nil {
		t.Errorf("FallOrFind=find: %v", err)
	}
	if err := (MeteoriteData{FallOrFind: ptr("crashed")}).Validate(); !errors.Is(err, ErrSpecimenTypeDataInvalid) {
		t.Errorf("FallOrFind=crashed: got %v", err)
	}
	if err := (MeteoriteData{TotalKnownWeightG: ptr(-5.0)}).Validate(); !errors.Is(err, ErrSpecimenTypeDataInvalid) {
		t.Errorf("TotalKnownWeightG<0: got %v", err)
	}
}

func TestQRSheetTemplateCapacity(t *testing.T) {
	cases := []struct {
		template QRSheetTemplate
		wantCap  int
		wantOK   bool
	}{
		{"avery-5160", 30, true},
		{"avery-5163", 10, true},
		{"avery-5164", 6, true},
		{"avery-22806", 12, true},
		{"avery-l7160", 21, true},
		{"avery-9999", 0, false},
		{"", 0, false},
	}
	for _, c := range cases {
		gotCap, gotOK := QRSheetTemplateCapacity(c.template)
		if gotCap != c.wantCap || gotOK != c.wantOK {
			t.Errorf("QRSheetTemplateCapacity(%q) = (%d, %v), want (%d, %v)",
				c.template, gotCap, gotOK, c.wantCap, c.wantOK)
		}
	}
}

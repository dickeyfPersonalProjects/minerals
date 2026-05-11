package domain

import (
	"encoding/json"
	"errors"
	"strings"
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

// TestMineralData_MarshalBooleans covers JSON marshalling of the three
// observable boolean properties (Radioactive, Magnetic, ReactsToAcid).
// Each is *bool so nil ("not recorded") must omit the key, false must
// round-trip as false, and true as true.
func TestMineralData_MarshalBooleans(t *testing.T) {
	t.Run("nil pointers omit the keys", func(t *testing.T) {
		b, err := json.Marshal(MineralData{})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		got := string(b)
		for _, key := range []string{"radioactive", "magnetic", "reacts_to_acid"} {
			if strings.Contains(got, `"`+key+`"`) {
				t.Errorf("empty MineralData JSON contains %q: %s", key, got)
			}
		}
	})

	t.Run("true values round-trip", func(t *testing.T) {
		in := MineralData{
			Radioactive:  ptr(true),
			Magnetic:     ptr(true),
			ReactsToAcid: ptr(true),
		}
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var out MineralData
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if out.Radioactive == nil || *out.Radioactive != true {
			t.Errorf("Radioactive: got %v, want *true", out.Radioactive)
		}
		if out.Magnetic == nil || *out.Magnetic != true {
			t.Errorf("Magnetic: got %v, want *true", out.Magnetic)
		}
		if out.ReactsToAcid == nil || *out.ReactsToAcid != true {
			t.Errorf("ReactsToAcid: got %v, want *true", out.ReactsToAcid)
		}
	})

	t.Run("false values round-trip", func(t *testing.T) {
		in := MineralData{
			Radioactive:  ptr(false),
			Magnetic:     ptr(false),
			ReactsToAcid: ptr(false),
		}
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var out MineralData
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if out.Radioactive == nil || *out.Radioactive != false {
			t.Errorf("Radioactive: got %v, want *false", out.Radioactive)
		}
		if out.Magnetic == nil || *out.Magnetic != false {
			t.Errorf("Magnetic: got %v, want *false", out.Magnetic)
		}
		if out.ReactsToAcid == nil || *out.ReactsToAcid != false {
			t.Errorf("ReactsToAcid: got %v, want *false", out.ReactsToAcid)
		}
	})
}

func TestMineralData_Validate_FluorescenceColors(t *testing.T) {
	// Empty per-wavelength slices remain valid (the v1 sparse default).
	if err := (MineralData{}).Validate(); err != nil {
		t.Errorf("empty fluorescence: %v", err)
	}

	// A representative valid color in each wavelength bucket.
	good := MineralData{
		FluorescenceSW: []string{"Red"},
		FluorescenceMW: []string{"Blue-green", "White"},
		FluorescenceLW: []string{"Cherry red"},
	}
	if err := good.Validate(); err != nil {
		t.Errorf("good fluorescence: %v", err)
	}

	// Every enum entry must round-trip through Validate.
	for color := range ValidFluorescenceColors {
		m := MineralData{FluorescenceSW: []string{color}}
		if err := m.Validate(); err != nil {
			t.Errorf("valid color %q rejected: %v", color, err)
		}
	}

	// An unknown color in any wavelength must trip ErrSpecimenTypeDataInvalid.
	for _, m := range []MineralData{
		{FluorescenceSW: []string{"Mauve"}},
		{FluorescenceMW: []string{"Cyan"}},       // generic display name, excluded
		{FluorescenceLW: []string{"Brown"}},      // excluded (not documented)
		{FluorescenceSW: []string{"Red", "BAD"}}, // mix valid + invalid
	} {
		err := m.Validate()
		if !errors.Is(err, ErrSpecimenTypeDataInvalid) {
			t.Errorf("invalid fluorescence color in %+v: got %v, want ErrSpecimenTypeDataInvalid", m, err)
		}
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

package domain

import (
	"strings"
	"testing"
)

func TestMineralData_Validate(t *testing.T) {
	good := 7.5
	if err := (MineralData{MohsHardness: &good}).Validate(); err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
	if err := (MineralData{}).Validate(); err != nil {
		t.Fatalf("empty struct should validate, got %v", err)
	}
	bad := 11.0
	if err := (MineralData{MohsHardness: &bad}).Validate(); err == nil {
		t.Fatal("expected error for mohs > 10")
	}
	tiny := -1.0
	if err := (MineralData{MohsHardness: &tiny}).Validate(); err == nil {
		t.Fatal("expected error for mohs < 0")
	}
}

func TestRockData_Validate(t *testing.T) {
	for _, v := range []string{"igneous", "sedimentary", "metamorphic"} {
		s := v
		if err := (RockData{RockType: &s}).Validate(); err != nil {
			t.Fatalf("rock_type %q should validate, got %v", v, err)
		}
	}
	bad := "andesite"
	err := (RockData{RockType: &bad}).Validate()
	if err == nil || !strings.Contains(err.Error(), "rock_type") {
		t.Fatalf("expected rock_type error, got %v", err)
	}
}

func TestMeteoriteData_Validate(t *testing.T) {
	fall := "fall"
	if err := (MeteoriteData{FallOrFind: &fall}).Validate(); err != nil {
		t.Fatalf("fall should validate, got %v", err)
	}
	bad := "discovered"
	if err := (MeteoriteData{FallOrFind: &bad}).Validate(); err == nil {
		t.Fatal("expected fall_or_find error")
	}
	neg := -1.0
	if err := (MeteoriteData{TotalKnownWeightG: &neg}).Validate(); err == nil {
		t.Fatal("expected weight error")
	}
	zero := 0.0
	if err := (MeteoriteData{TotalKnownWeightG: &zero}).Validate(); err != nil {
		t.Fatalf("zero weight should validate, got %v", err)
	}
}

func TestValidateTypeData_DispatchesByType(t *testing.T) {
	cases := []struct {
		name    string
		t       SpecimenType
		raw     string
		wantErr bool
	}{
		{"mineral ok", SpecimenMineral, `{"mohs_hardness": 6}`, false},
		{"mineral bad", SpecimenMineral, `{"mohs_hardness": 99}`, true},
		{"rock ok", SpecimenRock, `{"rock_type": "igneous"}`, false},
		{"rock bad", SpecimenRock, `{"rock_type": "x"}`, true},
		{"meteor ok", SpecimenMeteorite, `{"fall_or_find": "find"}`, false},
		{"meteor bad", SpecimenMeteorite, `{"fall_or_find": "x"}`, true},
		{"empty payload", SpecimenMineral, ``, false},
		{"unknown type", "amber", `{}`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateTypeData(c.t, []byte(c.raw))
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
		})
	}
}

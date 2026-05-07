package domain

import (
	"encoding/json"
	"fmt"
)

// ValidateForType unmarshals raw into the struct matching t and runs
// its Validate(). An empty raw payload is treated as an empty struct
// (every field optional in v1), which always validates.
func ValidateTypeData(t SpecimenType, raw []byte) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	switch t {
	case SpecimenMineral:
		var d MineralData
		if err := json.Unmarshal(raw, &d); err != nil {
			return fmt.Errorf("decode mineral type_data: %w", err)
		}
		return d.Validate()
	case SpecimenRock:
		var d RockData
		if err := json.Unmarshal(raw, &d); err != nil {
			return fmt.Errorf("decode rock type_data: %w", err)
		}
		return d.Validate()
	case SpecimenMeteorite:
		var d MeteoriteData
		if err := json.Unmarshal(raw, &d); err != nil {
			return fmt.Errorf("decode meteorite type_data: %w", err)
		}
		return d.Validate()
	default:
		return fmt.Errorf("unknown specimen type %q", t)
	}
}

// Validate enforces the v1 invariants on MineralData. Mohs hardness is
// the only constrained field; everything else is free-form text.
func (m MineralData) Validate() error {
	if m.MohsHardness != nil {
		if *m.MohsHardness < 0 || *m.MohsHardness > 10 {
			return fmt.Errorf("mohs_hardness out of range [0,10]")
		}
	}
	return nil
}

// Validate enforces the v1 invariants on RockData.
func (r RockData) Validate() error {
	if r.RockType != nil {
		switch *r.RockType {
		case "igneous", "sedimentary", "metamorphic":
		default:
			return fmt.Errorf("rock_type must be one of igneous|sedimentary|metamorphic")
		}
	}
	return nil
}

// Validate enforces the v1 invariants on MeteoriteData.
func (m MeteoriteData) Validate() error {
	if m.FallOrFind != nil {
		switch *m.FallOrFind {
		case "fall", "find":
		default:
			return fmt.Errorf("fall_or_find must be one of fall|find")
		}
	}
	if m.TotalKnownWeightG != nil && *m.TotalKnownWeightG < 0 {
		return fmt.Errorf("total_known_weight_g must be non-negative")
	}
	return nil
}

package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/smarniw/metamorphosis/internal/httpapi/gen"
)

// PATCH /sets/{id}: omitted leaves rir alone, an explicit null clears it, a number sets it.
func TestSetUpdateTellsOmittedNullAndValueApartForRir(t *testing.T) {
	for _, c := range []struct {
		body      string
		specified bool
		value     *int16
	}{
		{`{}`, false, nil},
		{`{"reps":3}`, false, nil},
		{`{"rir":null}`, true, nil},
		{`{"rir":2}`, true, ptr16(2)},
		{`{"rir":0}`, true, ptr16(0)},
	} {
		var u gen.WorkoutSetUpdate
		if err := json.Unmarshal([]byte(c.body), &u); err != nil {
			t.Fatalf("%s: %v", c.body, err)
		}
		got := workoutPatchRir(u)
		if got.Specified != c.specified || (got.Value == nil) != (c.value == nil) || (c.value != nil && *got.Value != *c.value) {
			t.Errorf("%s: got %+v", c.body, got)
		}
	}
	var u gen.WorkoutSetUpdate
	if err := json.Unmarshal([]byte(`{"rir":"x"}`), &u); err == nil {
		t.Error("a string rir should not decode")
	}
}

func ptr16(v int16) *int16 { return &v }

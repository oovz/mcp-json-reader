package query

import "testing"

func FuzzCompileQueries(f *testing.F) {
	seeds := []struct {
		language byte
		expr     string
	}{
		{0, ""},
		{0, "/items/0/name"},
		{0, "#/a~1b"},
		{0, "/bad~escape"},
		{1, "$"},
		{1, "$.items[*].name"},
		{1, "$..name"},
		{1, "$[?@.enabled == true]"},
	}
	for _, seed := range seeds {
		f.Add(seed.language, seed.expr)
	}

	f.Fuzz(func(t *testing.T, language byte, expression string) {
		if language%2 == 0 {
			plan, appErr := CompilePointer(expression)
			if (plan == nil) == (appErr == nil) {
				t.Fatalf("expected exactly one of a plan or an error, got plan=%v error=%v", plan, appErr)
			}
			return
		}

		plan, appErr := CompileJSONPath(expression)
		if (plan == nil) == (appErr == nil) {
			t.Fatalf("expected exactly one of a plan or an error, got plan=%v error=%v", plan, appErr)
		}
	})
}

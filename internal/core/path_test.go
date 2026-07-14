package core

import "testing"

func TestPathPointerEscapesPropertiesAndIndexes(t *testing.T) {
	path := Path{
		PropertySegment("orders"),
		IndexSegment(0),
		PropertySegment("a/b~c"),
		PropertySegment(""),
	}
	if got, want := path.Pointer(), "/orders/0/a~1b~0c/"; got != want {
		t.Fatalf("Pointer() = %q, want %q", got, want)
	}
	if got := (Path{}).Pointer(); got != "" {
		t.Fatalf("root Pointer() = %q, want empty string", got)
	}
}

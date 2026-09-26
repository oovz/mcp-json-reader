package engine

import "testing"

func TestJSONPathSegmentWhitespace(t *testing.T) {
	for _, space := range []string{" ", "\t", "\r", "\n", " \t\r\n"} {
		for _, expression := range []string{
			"$" + space + ".orders" + space + "[0]" + space + ".id",
			"$.orders" + space + "[?@.enabled == true]" + space + ".id",
			"$.orders[?@" + space + ".enabled == true].id",
			"$.orders[?@" + space + "['enabled'] == true].id",
			"$.orders[?@.flags" + space + "[0] == true].id",
		} {
			t.Run(expression, func(t *testing.T) {
				page, err := reviewExecute(t, `{"orders":[{"id":1,"enabled":true,"flags":[true]},{"id":2}]}`, expression)
				if err != nil || len(page.Items) != 1 || page.Items[0].Path != "/orders/0/id" || string(page.Items[0].Value) != "1" {
					t.Fatalf("page=%+v, error=%v; want /orders/0/id = 1", page, err)
				}
			})
		}
	}
}

func TestJSONPathNegationGrammarEvaluation(t *testing.T) {
	for _, tc := range []struct {
		expression string
		want       string
	}{
		{`$[?!@.active].id`, "2"},
		{`$[? ! (@.id == 1)].id`, "2"},
		{`$[?!(!@.active)].id`, "1"},
		{`$[?!@.active && @.id == 1 || @.id == 2].id`, "2"},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			page, err := reviewExecute(t, `[{"id":1,"active":false},{"id":2}]`, tc.expression)
			if err != nil || len(page.Items) != 1 || string(page.Items[0].Value) != tc.want {
				t.Fatalf("page=%+v, error=%v; want id %s", page, err, tc.want)
			}
		})
	}
}

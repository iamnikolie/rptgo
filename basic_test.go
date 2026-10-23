package main

import "testing"

type mapEnv map[string]any

func (m mapEnv) Lookup(ref string) (any, error) { return m[ref], nil }

func TestBasicSyntax(t *testing.T) {
	cases := []struct {
		src, typ string
		env      mapEnv
		want     any
	}{
		{"select case {Orders.Kind}\r\n    case \"Retail\"\r\n        formula = \"R\"\r\n    case \"Wholesale\"\r\n        formula = \"W\"\r\nend select",
			"String", mapEnv{"Orders.Kind": "Wholesale"}, "W"},
		{"select case {x}\n case \"a\"\n formula = \"A\"\nend select", "String", mapEnv{"x": "z"}, ""},
		{"if {Queue.Action}=\"DELETE\" then\n    formula = \"Y\"\nend if", "String", mapEnv{"Queue.Action": "ADD"}, ""},
		{"' guard\nformula=0\n\nif not isnull({#a}) then\n    formula = formula+{#a}\nend if\nif not isnull({#b}) then\n    formula = formula+{#b}\nend if",
			"Float64", mapEnv{"#a": 2.0}, 2.0},
		{"rem a note\ndim sTemp as string\nif Length({p})>0 then\n    stemp = {p}\nend if\nformula = stemp & \"!\"", "String", mapEnv{"p": "hi"}, "hi!"},
		{"if {a} = 1 then\n if {b} = 2 then\n  formula=\"x\"\n else\n  formula=\"y\"\n end if\nelseif {a} = 3 then\n formula=\"z\"\nelse\n formula=\"w\"\nend if",
			"String", mapEnv{"a": 3.0}, "z"},
		{"if {a} > 1 then formula = \"big\" else formula = \"small\"", "String", mapEnv{"a": 0.0}, "small"},
		{"select case {d}\n case 1, 7\n formula = \"weekend\"\n case else\n formula = \"weekday\"\nend select", "String", mapEnv{"d": 7.0}, "weekend"},
	}
	for _, c := range cases {
		if !isBasic(c.src) {
			t.Errorf("not detected as Basic: %q", c.src)
			continue
		}
		n, err := parseBasic(c.src, c.typ)
		if err != nil {
			t.Errorf("parse %q: %v", c.src, err)
			continue
		}
		got, err := eval(n, c.env, map[string]any{})
		if err != nil || got != c.want {
			t.Errorf("%q = %v, %v; want %v", c.src, got, err, c.want)
		}
	}
}

func TestCrystalSelect(t *testing.T) {
	src := "select {Visit.Status}\r\n    case \"N\":\"Normal\"\r\n    case \"M\", \"m\":\"Missed\"\r\n    default:\"Unk\"\r\n"
	n, err := parseFormula(src)
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{"N": "Normal", "m": "Missed", "Q": "Unk"} {
		got, err := eval(n, mapEnv{"Visit.Status": in}, map[string]any{})
		if err != nil || got != want {
			t.Errorf("%s: %v, %v; want %s", in, got, err, want)
		}
	}
}

package maintainability

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoisutolabs/goxlint/goxlinttest"
)

func TestDuplicateFunctionNormalization(t *testing.T) {
	var cases goxlinttest.PackageCases
	for _, tc := range []struct{ name, code string }{
		{"different operators", "func a(x int) int { return x+1 }; func b(y int) int { return y-1 }"},
		{"different literals", "func a(x int) int { return x+1 }; func b(y int) int { return y+2 }"},
		{"different types", "func a(x int) int { return x+1 }; func b(y int64) int64 { return y+1 }"},
		{"different globals", "var left,right int; func a(x int) int { return x+left }; func b(y int) int { return y+right }"},
		{"different calls", "var first,second func(int) int; func a(x int) int { return first(x) }; func b(y int) int { return second(y) }"},
		{"parameter order", "func a(x,y int) int { return x-y }; func b(n,m int) int { return m-n }"},
		{"different fields", "type T struct{ A,B int }; func a(t T) int { return t.A }; func b(r T) int { return r.B }"},
		{"different field keys", "type T struct{ A,B int }; func a(x int) T { return T{A:x} }; func b(y int) T { return T{B:y} }"},
		{"different receivers", "type T int; type U int; func (t T) A() int { return int(t) }; func (u U) B() int { return int(u) }"},
		{"different shadow binding", "func a(x int) int { y:=x; if x>0 { x:=2; _=y; _=x }; return y }; func b(n int) int { m:=n; if n>0 { n:=2; _=n; _=n }; return m }"},
		{"empty and bodyless", "func a() {}; func b() {}; func external()"},
	} {
		cases.Valid = append(cases.Valid, goxlinttest.PackageCase{
			Name:    tc.name,
			Files:   map[string]string{"p.go": "package p\n" + tc.code},
			Options: map[string]int{"minStatements": 1},
		})
	}
	for _, tc := range []struct {
		name, code string
		duplicates int
	}{
		{"renamed locals", "func first(x int) int { y := x+1; return y }; func second(a int) int { b := a+1; return b }", 1},
		{"format and comments", "func first(x int) int { /* why */ return x+1 }\nfunc second(y int) int {\n// a reason\nreturn y + 1\n}", 1},
		{"multiline trailing comma", "var call func(int,int) int; func a(x int) int { return call(x,1) }; func b(y int) int { return call(\ny,\n1,\n) }", 1},
		{"three copies", "func a(x int) int { return x+1 }; func b(y int) int { return y+1 }; func c(z int) int { return z+1 }", 2},
		{"named results", "func a(x int) (out int) { out=x+1; return }; func b(y int) (result int) { result=y+1; return }", 1},
		{"methods", "type T struct{ N int }; func (t T) First() int { return t.N+1 }; func (r T) Second() int { return r.N+1 }", 1},
		{"generics", "func a[T ~int](x T) T { return x+1 }; func b[U ~int](y U) U { return y+1 }", 1},
		{"nested closures", "func a(x int) func(int) int { return func(y int) int { return x+y } }; func b(n int) func(int) int { return func(m int) int { return n+m } }", 1},
		{"recursion", "func a(x int) int { if x==0 { return 1 }; return x*a(x-1) }; func b(y int) int { if y==0 { return 1 }; return y*b(y-1) }", 1},
		{"labels", "func a(x int) int {again: x--; if x>0 {goto again}; return x}; func b(y int) int {retry: y--; if y>0 {goto retry}; return y}", 1},
		{"shadowed locals", "func a(x int) int { y:=x; if x>0 { y:=2; _=y }; return y }; func b(n int) int { m:=n; if n>0 { m:=2; _=m }; return m }", 1},
	} {
		c := goxlinttest.PackageCase{
			Name:    tc.name,
			Files:   map[string]string{"p.go": "package p\n" + tc.code},
			Options: map[string]int{"minStatements": 1},
		}
		for range tc.duplicates {
			c.Errors = append(c.Errors, goxlinttest.PackageError{Filename: "p.go", MessageID: "duplicate"})
		}
		cases.Invalid = append(cases.Invalid, c)
	}
	goxlinttest.RunPackage(t, NoDuplicateFunctions, cases)
}

func TestDuplicatesAcrossFilesAndImportAliases(t *testing.T) {
	goxlinttest.RunPackage(t, NoDuplicateFunctions, goxlinttest.PackageCases{
		Invalid: []goxlinttest.PackageCase{{
			Files: map[string]string{
				"a.go": "package p\nimport s \"strings\"\nfunc first(x string) string { return s.TrimSpace(x) }",
				"b.go": "package p\nimport text \"strings\"\nfunc second(y string) string { return text.TrimSpace(y) }",
			},
			Options: map[string]int{"minStatements": 1},
			Errors:  []goxlinttest.PackageError{{Filename: "b.go", MessageID: "duplicate", Message: "Function second duplicates first at a.go:3:6.", Line: 3, Column: 6}},
		}},
	})
}

func TestDuplicateOptionsAndDisabledEvidence(t *testing.T) {
	sources := map[string]string{
		"a.go": "package p\nfunc first(x int) int { y:=x+1; return y }",
		"b.go": "package p\nfunc second(y int) int { z:=y+1; return z }",
	}
	goxlinttest.RunPackage(t, NoDuplicateFunctions, goxlinttest.PackageCases{
		Valid: []goxlinttest.PackageCase{
			{Name: "default minimum", Files: sources},
			{Name: "below minimum", Files: sources, Options: map[string]int{"minStatements": 3}},
		},
		Invalid: []goxlinttest.PackageCase{
			{Name: "at minimum", Files: sources, Options: map[string]int{"minStatements": 2}, Errors: []goxlinttest.PackageError{{Filename: "b.go", MessageID: "duplicate"}}},
			{Name: "disabled reference first", Files: sources, Options: map[string]int{"minStatements": 1}, FileOptions: map[string]any{"a.go": nil}, Errors: []goxlinttest.PackageError{{Filename: "b.go", MessageID: "duplicate"}}},
			{Name: "disabled reference last", Files: sources, Options: map[string]int{"minStatements": 1}, FileOptions: map[string]any{"b.go": nil}, Errors: []goxlinttest.PackageError{{Filename: "a.go", MessageID: "duplicate"}}},
		},
	})
	for _, tc := range []struct{ options, message string }{
		{`{"minStatements":0}`, "invalid options: minStatements must be at least 1"},
		{`{"minStatements":-1}`, "invalid options: minStatements must be at least 1"},
		{`{"typo":5}`, `invalid options for test/rule: json: unknown field "typo"`},
	} {
		goxlinttest.RunPackage(t, NoDuplicateFunctions, goxlinttest.PackageCases{
			Invalid: []goxlinttest.PackageCase{{
				Name:    tc.options,
				Files:   map[string]string{"p.go": "package p\nfunc f() {}"},
				Options: json.RawMessage(tc.options),
				Errors:  []goxlinttest.PackageError{{Filename: "p.go", Message: tc.message, Line: 1, Column: 1}},
			}},
		})
	}
}

func TestDuplicateSuppressionAndOverrides(t *testing.T) {
	goxlinttest.RunPackage(t, NoDuplicateFunctions, goxlinttest.PackageCases{
		Valid: []goxlinttest.PackageCase{{
			Files: map[string]string{
				"a.go": "package p\nfunc first(x int) int { return x+1 }",
				"b.go": "package p\n//goxlint:ignore test/rule shared implementation required\nfunc second(y int) int { return y+1 }",
			},
			Options: map[string]int{"minStatements": 1},
		}},
		Invalid: []goxlinttest.PackageCase{{
			Files: map[string]string{
				"a.go": "package p\nfunc first(x int) int { return x+1 }",
				"b.go": "package p\nfunc second(y int) int { return y+1 }",
				"c.go": "package p\nfunc third(z int) int { return z+1 }",
			},
			Options:     map[string]int{"minStatements": 1},
			FileOptions: map[string]any{"b.go": map[string]int{"minStatements": 2}},
			Errors:      []goxlinttest.PackageError{{Filename: "c.go", MessageID: "duplicate"}},
		}},
	})
}

func TestDuplicateTestFilePolicy(t *testing.T) {
	sources := map[string]string{
		"p.go":      "package p\nfunc original(x int) int { return x+1 }",
		"p_test.go": "package p\nfunc testCopy(y int) int { return y+1 }",
	}
	goxlinttest.RunPackage(t, NoDuplicateFunctions, goxlinttest.PackageCases{
		Valid: []goxlinttest.PackageCase{{
			Files: sources, Options: map[string]int{"minStatements": 1},
		}},
		Invalid: []goxlinttest.PackageCase{{
			Files: sources, Options: map[string]any{"minStatements": 1, "checkTests": true},
			Errors: []goxlinttest.PackageError{{Filename: "p_test.go", MessageID: "duplicate"}},
		}},
	})
}

func TestDuplicateDefaultMinimum(t *testing.T) {
	source := func(count int) string {
		return "package p\nfunc first(x int) int {" + strings.Repeat("x++;", count-1) + "return x}\nfunc second(y int) int {" + strings.Repeat("y++;", count-1) + "return y}"
	}
	goxlinttest.RunPackage(t, NoDuplicateFunctions, goxlinttest.PackageCases{
		Valid: []goxlinttest.PackageCase{{
			Name: "4 statements", Files: map[string]string{"p.go": source(4)},
		}},
		Invalid: []goxlinttest.PackageCase{{
			Name: "5 statements", Files: map[string]string{"p.go": source(5)},
			Errors: []goxlinttest.PackageError{{Filename: "p.go", MessageID: "duplicate"}},
		}},
	})
}
